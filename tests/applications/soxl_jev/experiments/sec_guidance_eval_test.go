package experiments_test

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/monitoring"
)

// Guidance-surprise pilot frozen by doc/engineering/P3_GUIDANCE_SURPRISE_PILOT.md.
// It pairs each earnings release with the same company's previous earnings
// release and reuses the SEC evaluation price stage and the single-shot runner.

var (
	guidanceFrom = time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	guidanceTo   = time.Date(2026, 6, 30, 23, 59, 0, 0, time.UTC)
)

const (
	guidancePriorWindow = 150 * 24 * time.Hour
	guidanceMaxRequest  = 192 << 10
)

var guidanceOptions = map[string][]string{
	"direction":             {"POSITIVE", "NEGATIVE", "MIXED", "NEUTRAL", "INSUFFICIENT"},
	"guidance_comparison":   {"ABOVE", "WITHIN", "BELOW", "MIXED", "NO_GUIDANCE"},
	"outlook_trend":         {"GROWTH", "FLAT", "DECLINE", "MIXED", "NO_OUTLOOK"},
	"expectation_direction": {"POSITIVE", "NEGATIVE", "MIXED", "NEUTRAL", "INSUFFICIENT"},
}

type guidancePair struct {
	Current secEvalCandidate
	Prior   *secEvalCandidate
}

// guidancePairs selects 2.02 filings in the pilot quarter and pairs each with
// the latest earlier 2.02 filing of the same company within the prior window.
func guidancePairs(submissions map[string][]byte) ([]guidancePair, error) {
	all := map[string][]monitoring.SECFiling{}
	for _, c := range secEvalCompanies {
		raw, ok := submissions[c.Ticker]
		if !ok {
			return nil, fmt.Errorf("GUIDANCE_SUBMISSIONS_MISSING %s", c.Ticker)
		}
		filings, err := monitoring.SECRecent8K(raw, []string{"2.02"}, guidanceFrom.Add(-guidancePriorWindow), guidanceTo)
		if err != nil {
			return nil, err
		}
		sort.SliceStable(filings, func(i, j int) bool { return filings[i].AcceptedAt.Before(filings[j].AcceptedAt) })
		all[c.Ticker] = filings
	}
	out := []guidancePair{}
	for _, c := range secEvalCompanies {
		for i, f := range all[c.Ticker] {
			if f.AcceptedAt.Before(guidanceFrom) {
				continue
			}
			p := guidancePair{Current: secEvalCandidate{c, f}}
			if i > 0 && f.AcceptedAt.Sub(all[c.Ticker][i-1].AcceptedAt) <= guidancePriorWindow {
				prior := secEvalCandidate{c, all[c.Ticker][i-1]}
				p.Prior = &prior
			}
			out = append(out, p)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Current.Filing, out[j].Current.Filing
		if a.AcceptedAt.Equal(b.AcceptedAt) {
			return a.Accession < b.Accession
		}
		return a.AcceptedAt.Before(b.AcceptedAt)
	})
	if len(out) > secEvalMaxSamples {
		out = out[:secEvalMaxSamples]
	}
	return out, nil
}

func TestGuidancePilotAcquire(t *testing.T) {
	lab := secEvalDir(t, "FACTORFORGE_GUIDANCE_LAB")
	if os.Mkdir(lab, 0700) != nil {
		t.Fatal("GUIDANCE_LAB_EXISTS")
	}
	frozenRateWrite(t, lab, "acquisition-started.json", map[string]any{"at": time.Now().UTC(), "from": guidanceFrom, "to": guidanceTo, "protocol": "doc/engineering/P3_GUIDANCE_SURPRISE_PILOT.md", "retries": 0})
	fetcher, pace := secEvalFetcher(t), &secEvalPacer{}
	submissions := map[string][]byte{}
	for _, c := range secEvalCompanies {
		submissions[c.Ticker], _ = pace.get(t, fetcher, fmt.Sprintf("https://data.sec.gov/submissions/CIK%010d.json", c.CIK))
	}
	pairs, err := guidancePairs(submissions)
	if err != nil {
		t.Fatal(err)
	}
	text := func(id string, c secEvalCandidate) (string, string) {
		folder := fmt.Sprintf("/Archives/edgar/data/%d/%s/", c.Company.CIK, strings.ReplaceAll(c.Filing.Accession, "-", ""))
		index, _ := pace.get(t, fetcher, "https://www.sec.gov"+folder+c.Filing.Accession+"-index.htm")
		href, found, err := monitoring.SECPressExhibit(index, folder)
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			return "", "NOT_RUN_NO_EX99"
		}
		raw, _ := pace.get(t, fetcher, "https://www.sec.gov"+href)
		if writeVenueReference(filepath.Join(lab, id+".htm"), raw) != nil {
			t.Fatal("GUIDANCE_WRITE")
		}
		body, err := secEvalText(raw)
		if err != nil {
			return "", "NOT_RUN_TEXT_UNAVAILABLE"
		}
		if writeVenueReference(filepath.Join(lab, id+".txt"), []byte(body)) != nil {
			t.Fatal("GUIDANCE_WRITE")
		}
		return body, ""
	}
	samples, rows := []secEvalSample{}, []map[string]any{}
	for i, p := range pairs {
		id := fmt.Sprintf("g-%02d", i+1)
		s := secEvalSample{ID: id, Ticker: p.Current.Company.Ticker, CIK: p.Current.Company.CIK, Accession: p.Current.Filing.Accession, AcceptedAt: p.Current.Filing.AcceptedAt, Items: p.Current.Filing.Items, Status: "READY"}
		row := map[string]any{"id": id, "accession": s.Accession}
		if p.Prior == nil {
			s.Status = "NOT_RUN_NO_PRIOR"
		} else {
			row["prior_accession"], row["prior_accepted_at"] = p.Prior.Filing.Accession, p.Prior.Filing.AcceptedAt
			current, status := text(id, p.Current)
			prior := ""
			if status == "" {
				prior, status = text(id+"-prior", *p.Prior)
			}
			if status != "" {
				s.Status = status
			} else {
				s.TextHash, s.TextBytes = d.ContentDigest([]byte(current)), len(current)
				row["prior_text_hash"], row["prior_text_bytes"] = d.ContentDigest([]byte(prior)), len(prior)
			}
		}
		samples, rows = append(samples, s), append(rows, row)
	}
	frozenRateWrite(t, lab, "samples.json", map[string]any{"complete": true, "mode": "guidance-pilot", "samples": samples})
	frozenRateWrite(t, lab, "pairs.json", map[string]any{"rows": rows})
	t.Logf("guidance pilot acquisition: samples=%d", len(samples))
}

// Prices come from TestSECDirectEvalPrices on the same laboratory; requests are
// built only after prices.json exists and is sealed.
func TestGuidancePilotPrepare(t *testing.T) {
	lab := secEvalDir(t, "FACTORFORGE_GUIDANCE_LAB")
	var set struct {
		Samples []secEvalSample `json:"samples"`
	}
	samplesRaw := financialRead(t, lab, "samples.json", &set)
	var pairs struct {
		Rows []map[string]any `json:"rows"`
	}
	pairsRaw := financialRead(t, lab, "pairs.json", &pairs)
	pricesRaw := financialRead(t, lab, "prices.json", nil)
	var questions map[string]json.RawMessage
	qRaw := financialRead(t, lab, "questions.json", &questions)
	if len(questions) != len(guidanceOptions) {
		t.Fatal("GUIDANCE_QUESTIONS")
	}
	for key := range guidanceOptions {
		if _, ok := questions[key]; !ok {
			t.Fatal("GUIDANCE_QUESTIONS")
		}
	}
	requests, hashes, notRun := []string{}, map[string]string{}, map[string]string{}
	for _, s := range set.Samples {
		if s.Status != "READY" {
			continue
		}
		current, err1 := os.ReadFile(filepath.Join(lab, s.ID+".txt"))
		prior, err2 := os.ReadFile(filepath.Join(lab, s.ID+"-prior.txt"))
		if err1 != nil || err2 != nil || d.ContentDigest(current) != s.TextHash {
			t.Fatal("GUIDANCE_TEXT_CHANGED")
		}
		var r struct {
			Model string `json:"model"`
			State struct {
				News     string `json:"news"`
				Previous string `json:"previous_news"`
			} `json:"state"`
			Questions map[string]json.RawMessage `json:"questions"`
		}
		r.Model, r.State.News, r.State.Previous, r.Questions = "jev-1.13.0", string(current), string(prior), questions
		raw := mustNewsJSON(r)
		if len(raw) > guidanceMaxRequest {
			notRun[s.ID] = "NOT_RUN_REQUEST_OVER_LIMIT"
			continue
		}
		name := s.ID + "-request.json"
		frozenRateWrite(t, lab, name, r)
		saved, _ := os.ReadFile(filepath.Join(lab, name))
		requests, hashes[name] = append(requests, name), d.ContentDigest(saved)
	}
	frozenRateWrite(t, lab, "plan.json", map[string]any{"model": "jev-1.13.0", "max_requests": len(requests), "timeout_seconds": 20, "max_bytes": guidanceMaxRequest, "requests": requests})
	planRaw, _ := os.ReadFile(filepath.Join(lab, "plan.json"))
	// The runner records a development-label slot; here it holds the sealed prices.
	if writeVenueReference(filepath.Join(lab, "labels.json"), pricesRaw) != nil {
		t.Fatal("GUIDANCE_WRITE")
	}
	code, _ := os.ReadFile("sec_guidance_eval_test.go")
	runner, _ := os.ReadFile("semantic_lab_test.go")
	protocol, _ := os.ReadFile("../../../../doc/engineering/P3_GUIDANCE_SURPRISE_PILOT.md")
	frozenRateWrite(t, lab, "sealed.json", map[string]any{"at": time.Now().UTC(), "samples_hash": d.ContentDigest(samplesRaw), "pairs_hash": d.ContentDigest(pairsRaw), "prices_hash": d.ContentDigest(pricesRaw), "questions_hash": d.ContentDigest(qRaw), "plan_hash": d.ContentDigest(planRaw), "code_hash": d.ContentDigest(code), "runner_hash": d.ContentDigest(runner), "protocol_hash": d.ContentDigest(protocol), "requests": hashes, "not_run": notRun})
	t.Logf("guidance pilot sealed: requests=%d not_run=%d", len(requests), len(notRun))
}

func TestRunSealedGuidancePilot(t *testing.T) {
	lab := secEvalDir(t, "FACTORFORGE_GUIDANCE_LAB")
	var seal struct {
		Prices    string            `json:"prices_hash"`
		Questions string            `json:"questions_hash"`
		Plan      string            `json:"plan_hash"`
		Code      string            `json:"code_hash"`
		Runner    string            `json:"runner_hash"`
		Protocol  string            `json:"protocol_hash"`
		Requests  map[string]string `json:"requests"`
	}
	financialRead(t, lab, "sealed.json", &seal)
	files := map[string]string{"prices.json": seal.Prices, "questions.json": seal.Questions, "plan.json": seal.Plan}
	for name, hash := range seal.Requests {
		files[name] = hash
	}
	for name, hash := range files {
		if raw, err := os.ReadFile(filepath.Join(lab, name)); err != nil || d.ContentDigest(raw) != hash {
			t.Fatal("GUIDANCE_PREFLIGHT_CHANGED", name)
		}
	}
	for name, hash := range map[string]string{"sec_guidance_eval_test.go": seal.Code, "semantic_lab_test.go": seal.Runner, "../../../../doc/engineering/P3_GUIDANCE_SURPRISE_PILOT.md": seal.Protocol} {
		if raw, err := os.ReadFile(name); err != nil || d.ContentDigest(raw) != hash {
			t.Fatal("GUIDANCE_PREFLIGHT_CODE_CHANGED", name)
		}
	}
	t.Setenv("FACTORFORGE_SEMANTIC_LAB", lab)
	TestJevSemanticMethodProbe(t)
}

// guidanceReplyValid applies the v2.5.4 distribution rule: exact decimal sum
// within 0.01 of one, every probability in [0, 1] and the full option set.
func guidanceReplyValid(r directNewsReply) bool {
	if r.Model != "jev-1.13.0" || len(r.Answers) != len(guidanceOptions) {
		return false
	}
	one, tolerance := big.NewRat(1, 1), big.NewRat(1, 100)
	for key, opts := range guidanceOptions {
		ans, ok := r.Answers[key]
		if !ok || ans.Type != "choice" || ans.Choice == nil || !d.Has(opts, *ans.Choice) || ans.Score != nil || len(ans.Probabilities) != len(opts) {
			return false
		}
		sum := new(big.Rat)
		for _, opt := range opts {
			value, exists := ans.Probabilities[opt]
			p, parsed := new(big.Rat).SetString(string(value))
			if !exists || !parsed || p.Sign() < 0 || p.Cmp(one) > 0 {
				return false
			}
			sum.Add(sum, p)
		}
		if new(big.Rat).Abs(new(big.Rat).Sub(sum, one)).Cmp(tolerance) > 0 {
			return false
		}
	}
	return true
}

func guidanceSign(key, choice string) (float64, bool) {
	switch {
	case key == "guidance_comparison" && choice == "ABOVE", key != "guidance_comparison" && choice == "POSITIVE":
		return 1, true
	case key == "guidance_comparison" && choice == "BELOW", key != "guidance_comparison" && choice == "NEGATIVE":
		return -1, true
	}
	return 0, false
}

func guidanceEvaluate(replies map[string]directNewsReply, abnormal map[string]float64) map[string]any {
	summary := map[string]any{"accuracy_claimed": false, "production_admission": false}
	ids := []string{}
	for id := range replies {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, key := range []string{"direction", "expectation_direction", "guidance_comparison"} {
		agree, n := 0, 0
		xs, ys := []float64{}, []float64{}
		counts := map[string]int{}
		for _, id := range ids {
			choice := *replies[id].Answers[key].Choice
			counts[choice]++
			ret, priced := abnormal[id]
			if sign, ok := guidanceSign(key, choice); ok && priced && ret != 0 {
				n++
				if sign*ret > 0 {
					agree++
				}
				xs, ys = append(xs, sign), append(ys, ret)
			}
		}
		entry := map[string]any{"directional_priced": n, "agree": agree, "choices": counts, "spearman": nil}
		if rho, ok := secSpearman(xs, ys); ok {
			entry["spearman"] = rho
		}
		summary[key] = entry
	}
	positive, priced := 0, 0
	for _, id := range ids {
		if ret, ok := abnormal[id]; ok {
			priced++
			if ret > 0 {
				positive++
			}
		}
	}
	summary["all_positive_baseline"] = map[string]int{"priced": priced, "agree": positive}
	return summary
}

func TestEvaluateGuidancePilot(t *testing.T) {
	lab := secEvalDir(t, "FACTORFORGE_GUIDANCE_LAB")
	var plan struct {
		Requests []string `json:"requests"`
	}
	financialRead(t, lab, "plan.json", &plan)
	var prices struct {
		Rows []struct {
			ID       string   `json:"id"`
			Status   string   `json:"status"`
			Abnormal *float64 `json:"abnormal_return"`
		} `json:"rows"`
	}
	financialRead(t, lab, "prices.json", &prices)
	abnormal := map[string]float64{}
	for _, r := range prices.Rows {
		if r.Status == "OK" && r.Abnormal != nil {
			abnormal[r.ID] = *r.Abnormal
		}
	}
	replies, rows := map[string]directNewsReply{}, []map[string]any{}
	for i, name := range plan.Requests {
		id := strings.TrimSuffix(name, "-request.json")
		row := map[string]any{"id": id, "status": "INVALID"}
		raw, err := os.ReadFile(filepath.Join(lab, fmt.Sprintf("case-%03d-response.json", i+1)))
		var r directNewsReply
		if err == nil && d.DecodePrivate(raw, &r) == nil && guidanceReplyValid(r) {
			replies[id] = r
			answers := map[string]string{}
			for key := range guidanceOptions {
				answers[key] = *r.Answers[key].Choice
			}
			row["status"], row["answers"] = "VALID", answers
		}
		if ret, ok := abnormal[id]; ok {
			row["abnormal_return"] = strconv.FormatFloat(ret, 'f', 6, 64)
		}
		rows = append(rows, row)
	}
	summary := guidanceEvaluate(replies, abnormal)
	summary["valid"], summary["requests"] = len(replies), len(plan.Requests)
	frozenRateWrite(t, lab, "evaluation.json", map[string]any{"at": time.Now().UTC(), "summary": summary, "rows": rows})
	t.Logf("guidance pilot: %s", mustNewsJSON(summary))
}

func TestGuidancePilotOfflineBoundaries(t *testing.T) {
	sub := func(rows ...[4]string) []byte {
		cols := [4][]string{}
		for _, r := range rows {
			for i := range r {
				cols[i] = append(cols[i], strconv.Quote(r[i]))
			}
		}
		return []byte(fmt.Sprintf(`{"filings":{"recent":{"form":[%s],"accessionNumber":[%s],"acceptanceDateTime":[%s],"items":[%s]}}}`, strings.Join(cols[0], ","), strings.Join(cols[1], ","), strings.Join(cols[2], ","), strings.Join(cols[3], ",")))
	}
	submissions := map[string][]byte{}
	for _, c := range secEvalCompanies {
		submissions[c.Ticker] = sub()
	}
	submissions["NVDA"] = sub([4]string{"8-K", "0001045810-26-000020", "2026-05-28T20:20:00Z", "2.02,9.01"}, [4]string{"8-K", "0001045810-26-000010", "2026-02-25T21:20:00Z", "2.02,9.01"}, [4]string{"8-K", "0001045810-26-000015", "2026-04-10T12:00:00Z", "8.01"})
	submissions["MU"] = sub([4]string{"8-K", "0000723125-26-000009", "2026-06-25T20:05:00Z", "2.02"}, [4]string{"8-K", "0000723125-26-000001", "2025-12-17T21:00:00Z", "2.02"})
	pairs, err := guidancePairs(submissions)
	// NVDA pairs with its February release; MU's previous release is older than 150 days.
	if err != nil || len(pairs) != 2 || pairs[0].Current.Company.Ticker != "NVDA" || pairs[0].Prior == nil || pairs[0].Prior.Filing.Accession != "0001045810-26-000010" || pairs[1].Prior != nil {
		t.Fatal("guidance pairing", err, pairs)
	}
	choice := func(v string) *string { return &v }
	reply := directNewsReply{Model: "jev-1.13.0", Answers: map[string]directNewsAnswer{}}
	set := func(key, picked string, probs ...string) {
		p := map[string]json.Number{}
		for i, opt := range guidanceOptions[key] {
			p[opt] = json.Number(probs[i])
		}
		reply.Answers[key] = directNewsAnswer{Type: "choice", Choice: choice(picked), Probabilities: p}
	}
	set("direction", "POSITIVE", "0.6", "0.1", "0.1", "0.1", "0.09")
	set("guidance_comparison", "ABOVE", "0.7", "0.2", "0.1", "0", "0")
	set("outlook_trend", "GROWTH", "0.8", "0.1", "0.1", "0", "0")
	set("expectation_direction", "NEGATIVE", "0.2", "0.7", "0.1", "0", "0")
	if !guidanceReplyValid(reply) {
		t.Fatal("rounded valid reply rejected")
	}
	set("outlook_trend", "GROWTH", "0.8", "0.1", "0.08", "0", "0")
	if guidanceReplyValid(reply) {
		t.Fatal("distribution off by 0.02 accepted")
	}
	set("outlook_trend", "GROWTH", "0.8", "0.1", "0.1", "0", "0")
	summary := guidanceEvaluate(map[string]directNewsReply{"g-01": reply}, map[string]float64{"g-01": -0.05})
	if fmt.Sprint(summary["expectation_direction"].(map[string]any)["agree"]) != "1" || fmt.Sprint(summary["direction"].(map[string]any)["agree"]) != "0" || fmt.Sprint(summary["guidance_comparison"].(map[string]any)["agree"]) != "0" {
		t.Fatal("guidance evaluation", summary)
	}
}
