package workers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/analysis"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/monitoring"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
	tdto "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
)

// DynamicStore is the v2.9.0 append-only record and CAS state store.
type DynamicStore interface {
	AppendRecord(ctx context.Context, kind, id string, at time.Time, payload any) error
	Records(ctx context.Context, kind string, since, until time.Time, limit int) ([]json.RawMessage, error)
	LoadState(ctx context.Context, key string, target any) (int64, error)
	SaveState(ctx context.Context, key string, expected int64, at time.Time, payload any) error
}
type NewsPoller interface {
	Poll(ctx context.Context) []monitoring.FeedResult
}
type ModelGenerator interface {
	Generate(ctx context.Context, id, input string) (d.SemanticGeneration, error)
}
type NamedModel struct {
	Name      string
	Generator ModelGenerator
}
type JudgmentFramework interface {
	Version(ctx context.Context, object string) (int, error)
	RegisterEvent(ctx context.Context, request dto.EventCommand, revision bool) (dto.Event, error)
	Submit(ctx context.Context, request dto.ScoreCommand) (dto.AdmissionReceipt, error)
}
// JudgmentSource reads another instance's judgments in follower mode
// (S3-034): strictly after since, oldest first.
type JudgmentSource interface {
	After(ctx context.Context, since time.Time) ([]d.SourcedJudgment, error)
}
type Alerter interface {
	Alert(ctx context.Context, code, detail string)
}

// JudgmentSettings are registered experiment values (P3 design 28.2/28.3).
type JudgmentSettings struct {
	ObjectID, SubjectID, EventType        string
	RecentWindow, OlderWindow, MaxNewsAge time.Duration
	MaxRecent, MaxOlder                   int
	ImpactByConfidence                    [5]string
	Credibility, Relevance, Quality       string
	Rubric, Calibration, Verification     string
	ProducerID, ProducerVersion           string
	MinHalfLifeHours, MaxHalfLifeHours    int
	Licence                               string
}

type NewsSourceState struct {
	LastSuccess time.Time `json:"last_success"`
	Failing     bool      `json:"failing"`
	LastError   string    `json:"last_error,omitempty"`
}

type JudgmentWorker struct {
	Store     DynamicStore
	Framework JudgmentFramework
	News      NewsPoller
	Models    []NamedModel
	Template  analysis.JudgmentTemplate
	Clock     func() time.Time
	Alerts    Alerter
	Settings  JudgmentSettings
	// Source and FollowSince select follower mode (S3-034, v2.13.0): Follow
	// replaces Collect and Judge; no feed is polled and no model is called.
	Source      JudgmentSource
	FollowSince time.Time
}

// FollowState is the follower's cursor: the newest source judgment copied.
type FollowState struct {
	LastAt time.Time `json:"last_at"`
	LastID string    `json:"last_id"`
}

// Follow copies each new source judgment and the news it cites into this
// instance's records, then applies it exactly as Judge would (S3-025):
// stale-news rounds are only recorded, everything else goes through apply.
func (w *JudgmentWorker) Follow(ctx context.Context) error {
	if w.Source == nil || w.FollowSince.IsZero() {
		return d.Fail("JUDGMENT_SOURCE_CONFIGURATION_REQUIRED", 503)
	}
	var cursor FollowState
	cv, err := w.Store.LoadState(ctx, "FOLLOW", &cursor)
	if err != nil {
		return err
	}
	since := cursor.LastAt
	if since.Before(w.FollowSince) {
		since = w.FollowSince
	}
	var state d.InjectionState
	version, err := w.Store.LoadState(ctx, "INJECTION", &state)
	if err != nil {
		return err
	}
	// A crash between copying and applying leaves the newest copy unapplied.
	if err = w.resume(ctx, &state, &version); err != nil {
		return err
	}
	rows, err := w.Source.After(ctx, since)
	if err != nil {
		return err
	}
	for _, row := range rows {
		j := row.Judgment
		for _, item := range row.Items {
			if err := w.Store.AppendRecord(ctx, "NEWS", item.ItemID, item.ReceivedAt, item); err != nil {
				var known *d.Error
				if !errors.As(err, &known) || known.Code != "DYNAMIC_RECORD_CONFLICT" {
					return err
				}
			}
		}
		if err = w.Store.AppendRecord(ctx, "JUDGMENT", j.JudgmentID, j.At, j); err != nil {
			return err
		}
		if j.Reason != "NEWS_STALE" && j.JudgmentID > state.LastJudgment {
			recent, older, _, err := w.inputs(ctx, j.At)
			if err != nil {
				return err
			}
			if err = w.apply(ctx, &state, version, j, recent, older); err != nil {
				return err
			}
			if version, err = w.Store.LoadState(ctx, "INJECTION", &state); err != nil {
				return err
			}
		}
		next := FollowState{LastAt: j.At, LastID: j.JudgmentID}
		if err = w.Store.SaveState(ctx, "FOLLOW", cv, j.At, next); err != nil {
			return err
		}
		if cv, err = w.Store.LoadState(ctx, "FOLLOW", &cursor); err != nil {
			return err
		}
	}
	return nil
}

// Collect polls every registered feed once and records new items.
func (w *JudgmentWorker) Collect(ctx context.Context) error {
	now := w.Clock().UTC().Truncate(time.Second)
	sources := map[string]NewsSourceState{}
	version, err := w.Store.LoadState(ctx, "NEWS_SOURCES", &sources)
	if err != nil {
		return err
	}
	for _, r := range w.News.Poll(ctx) {
		s := sources[r.SourceID]
		if r.Err != nil {
			s.Failing, s.LastError = true, errCode(r.Err)
			sources[r.SourceID] = s
			continue
		}
		s.Failing, s.LastError, s.LastSuccess = false, "", now
		sources[r.SourceID] = s
		for _, item := range r.Items {
			if err := w.Store.AppendRecord(ctx, "NEWS", item.ItemID, item.ReceivedAt, item); err != nil {
				var known *d.Error
				// A re-seen item keeps its first capture; the conflict is expected.
				if !errors.As(err, &known) || known.Code != "DYNAMIC_RECORD_CONFLICT" {
					return err
				}
			}
		}
	}
	return w.Store.SaveState(ctx, "NEWS_SOURCES", version, now, sources)
}

func errCode(err error) string {
	var known *d.Error
	if errors.As(err, &known) {
		return known.Code
	}
	return "SOURCE_FETCH_UNAVAILABLE"
}

// Judge runs one judgment and applies its framework writes (S3-024/S3-025).
func (w *JudgmentWorker) Judge(ctx context.Context) (d.Judgment, error) {
	now := w.Clock().UTC().Truncate(time.Second)
	id := "j-" + now.Format("20060102T150405Z")
	j := d.Judgment{JudgmentID: id, At: now}
	var state d.InjectionState
	version, err := w.Store.LoadState(ctx, "INJECTION", &state)
	if err != nil {
		return j, err
	}
	if err = w.resume(ctx, &state, &version); err != nil {
		return j, err
	}
	recent, older, fresh, err := w.inputs(ctx, now)
	if err != nil {
		return j, err
	}
	if !fresh {
		j.Reason = "NEWS_STALE"
		w.alert(ctx, "NEWS_STALE", id)
		return j, w.Store.AppendRecord(ctx, "JUDGMENT", id, now, j)
	}
	var previous *d.JudgmentOutput
	if last, _ := w.latestJudgment(ctx, now); last != nil {
		previous = last.Output
	}
	text, ids, hash := analysis.RenderJudgment(w.Template, now, previous, recent, older)
	j.InputHash, j.InputIDs = hash, ids
	for _, m := range w.Models {
		g, err := m.Generator.Generate(ctx, id+"-"+m.Name, text)
		if err != nil {
			j.Errors = append(j.Errors, m.Name+": "+errCode(err))
			continue
		}
		j.Provider, j.Model, j.PromptHash, j.Raw = g.Provider, g.Model, g.PromptHash, truncate(g.Text, 4000)
		out, err := d.ParseJudgment(g.Text, ids)
		if err != nil {
			j.Errors = append(j.Errors, m.Name+": "+errCode(err))
			continue
		}
		j.Output, j.OK = &out, true
		break
	}
	if !j.OK {
		j.Reason = "MODELS_FAILED"
		w.alert(ctx, "JUDGMENT_MODELS_FAILED", id)
	}
	if err = w.Store.AppendRecord(ctx, "JUDGMENT", id, now, j); err != nil {
		return j, err
	}
	return j, w.apply(ctx, &state, version, j, recent, older)
}

// resume re-applies the newest judgment when a crash left it unprocessed.
func (w *JudgmentWorker) resume(ctx context.Context, state *d.InjectionState, version *int64) error {
	last, err := w.latestJudgment(ctx, w.Clock().UTC())
	if err != nil || last == nil || !last.OK || last.JudgmentID == state.LastJudgment || last.JudgmentID <= state.LastJudgment {
		return err
	}
	recent, older, _, err := w.inputs(ctx, last.At)
	if err != nil {
		return err
	}
	if err = w.apply(ctx, state, *version, *last, recent, older); err != nil {
		return err
	}
	*version, err = w.Store.LoadState(ctx, "INJECTION", state)
	return err
}

func (w *JudgmentWorker) apply(ctx context.Context, state *d.InjectionState, version int64, j d.Judgment, recent, older []d.NewsItem) error {
	next, actions := d.DecideInjection(*state, j)
	items := map[string]d.NewsItem{}
	for _, x := range append(append([]d.NewsItem{}, recent...), older...) {
		items[x.ItemID] = x
	}
	for _, a := range actions {
		if err := w.Store.AppendRecord(ctx, "ACTION", j.JudgmentID+"-"+a.Kind+"-"+strconv.Itoa(len(a.Targets))+"-"+a.EventID, j.At, a); err != nil {
			return err
		}
		var err error
		switch a.Kind {
		case "INJECT":
			err = w.inject(ctx, &next, j, a, items)
		case "INVALIDATE":
			err = w.retract(ctx, &next, j, a)
		case "REVISE":
			err = w.revise(ctx, &next, j, a)
		}
		if err != nil {
			return err
		}
	}
	return w.Store.SaveState(ctx, "INJECTION", version, j.At, next)
}

func (w *JudgmentWorker) inject(ctx context.Context, state *d.InjectionState, j d.Judgment, a d.InjectionAction, items map[string]d.NewsItem) error {
	s := w.Settings
	event := dto.Event{EventID: a.EventID, FamilyID: a.EventID, FactVersion: 1, Relation: "NEW", SubjectID: s.SubjectID, EventType: s.EventType, OccurredAt: j.At, FirstPublicAt: j.At, EvidenceRefs: []dto.EvidenceRef{}, Claims: []dto.Claim{}, ObjectIDs: []string{s.ObjectID}, State: "QUARANTINED"}
	weight, _ := tdto.ParseDecimal("1")
	for n, id := range a.ItemIDs {
		x, ok := items[id]
		if !ok {
			return d.Fail("JUDGMENT_ITEM_UNAVAILABLE", 503)
		}
		if x.PublishedAt.Before(event.FirstPublicAt) {
			event.FirstPublicAt = x.PublishedAt
		}
		h := sha256.Sum256([]byte(x.Source + "|" + x.Title + "|" + x.Summary))
		event.EvidenceRefs = append(event.EvidenceRefs, dto.EvidenceRef{EvidenceID: id, ContentHash: hex.EncodeToString(h[:]), SourceID: x.Source, LicenceRef: s.Licence, FirstPublicAt: x.PublishedAt, ReceivedAt: x.ReceivedAt, AvailableAt: x.ReceivedAt, SpanRefs: []string{"headline"}, VerificationRef: s.Verification})
		event.Claims = append(event.Claims, dto.Claim{ClaimID: a.EventID + "-c" + strconv.Itoa(n+1), NormalizedFact: "judgment:" + a.EventID + ":" + id, SubjectID: s.SubjectID, EconomicItem: s.EventType, Period: a.EventID, FactTime: x.PublishedAt, NumbersWithUnits: map[string]string{}, EvidenceRefs: []string{id}, VerifiedAt: j.At, Weight: weight, VerificationManifest: s.Rubric})
	}
	if err := w.versioned(ctx, func(c dto.Command) error {
		_, err := w.Framework.RegisterEvent(ctx, dto.EventCommand{Command: c, Event: event}, false)
		return err
	}, a.EventID, "NEWS_JUDGMENT_EVENT"); err != nil {
		return err
	}
	scoreID := "score-" + a.EventID
	score, err := w.score(scoreID, a.EventID, 1, nil, a, j, a.ItemIDs)
	if err != nil {
		return err
	}
	if err = w.versioned(ctx, func(c dto.Command) error {
		_, err := w.Framework.Submit(ctx, dto.ScoreCommand{Command: c, Score: score})
		return err
	}, scoreID, "NEWS_JUDGMENT_SCORE"); err != nil {
		return err
	}
	raw, _ := json.Marshal(event)
	state.Events[a.EventID] = d.InjectedEvent{Event: raw, ScoreID: scoreID, ScoreVersion: 1, HorizonHours: a.HorizonHours}
	return nil
}

func (w *JudgmentWorker) score(id, event string, version int, previous *string, a d.InjectionAction, j d.Judgment, evidence []string) (dto.ScoreSubmission, error) {
	s := w.Settings
	parse := func(v string) (*tdto.Decimal, error) {
		x, err := tdto.ParseDecimal(v)
		return &x, err
	}
	direction := d.DirectionSign(a.Direction)
	impact, err := tdto.ParseDecimal(s.ImpactByConfidence[a.Confidence-1])
	if err != nil {
		return dto.ScoreSubmission{}, d.Fail("JUDGMENT_CONFIGURATION_INVALID", 503)
	}
	hours := a.HorizonHours
	if hours < s.MinHalfLifeHours {
		hours = s.MinHalfLifeHours
	}
	if hours > s.MaxHalfLifeHours {
		hours = s.MaxHalfLifeHours
	}
	cred, e1 := parse(s.Credibility)
	rel, e2 := parse(s.Relevance)
	quality, e3 := parse(s.Quality)
	one, _ := parse("1")
	zero, _ := parse("0")
	half, e4 := parse(strconv.Itoa(hours * 3600))
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
		return dto.ScoreSubmission{}, d.Fail("JUDGMENT_CONFIGURATION_INVALID", 503)
	}
	kind := "INITIAL"
	if previous != nil {
		kind = "REVISION"
	}
	sort.Strings(evidence)
	return dto.ScoreSubmission{SubmissionID: id, EventID: event, FactVersion: 1, ObjectID: s.ObjectID, ScoreVersion: version, PreviousScoreID: previous, RevisionKind: kind, Vector: dto.ScoreVector{Direction: direction, ImpactPoints: impact, Credibility: cred, Relevance: rel, Novelty: one, ExpectationCoverage: zero, PrepricingFraction: zero, ExpectedHalfLife: half, QualityScore: quality, UnknownFields: []string{}}, EvidenceRefs: evidence, ProducerID: s.ProducerID, ProducerVersion: s.ProducerVersion, RubricVersion: s.Rubric, CalibrationVersion: s.Calibration, CompletedAt: j.At, InputManifestHash: j.InputHash}, nil
}

func (w *JudgmentWorker) retract(ctx context.Context, state *d.InjectionState, j d.Judgment, a d.InjectionAction) error {
	for _, target := range a.Targets {
		rec, ok := state.Events[target]
		if !ok || rec.Retracted {
			continue
		}
		var event dto.Event
		if json.Unmarshal(rec.Event, &event) != nil {
			return d.Fail("DYNAMIC_STATE_INVALID", 503)
		}
		event.FactVersion, event.Relation, event.ParentEventID = 2, "RETRACTION", nil
		if err := w.versioned(ctx, func(c dto.Command) error {
			_, err := w.Framework.RegisterEvent(ctx, dto.EventCommand{Command: c, Event: event}, true)
			return err
		}, target+"-retract", a.Reason); err != nil {
			return err
		}
		rec.Retracted = true
		state.Events[target] = rec
	}
	return nil
}

func (w *JudgmentWorker) revise(ctx context.Context, state *d.InjectionState, j d.Judgment, a d.InjectionAction) error {
	for _, target := range a.Targets {
		rec, ok := state.Events[target]
		if !ok || rec.Retracted {
			continue
		}
		var event dto.Event
		if json.Unmarshal(rec.Event, &event) != nil {
			return d.Fail("DYNAMIC_STATE_INVALID", 503)
		}
		evidence := []string{}
		for _, e := range event.EvidenceRefs {
			evidence = append(evidence, e.EvidenceID)
		}
		id := rec.ScoreID + "-r" + strconv.Itoa(rec.ScoreVersion+1)
		previous := rec.ScoreID
		if rec.ScoreVersion > 1 {
			previous = rec.ScoreID + "-r" + strconv.Itoa(rec.ScoreVersion)
		}
		// A revision keeps the original half-life; only the impact changes.
		revision := a
		revision.HorizonHours = rec.HorizonHours
		score, err := w.score(id, target, rec.ScoreVersion+1, &previous, revision, j, evidence)
		if err != nil {
			return err
		}
		if err = w.versioned(ctx, func(c dto.Command) error {
			_, err := w.Framework.Submit(ctx, dto.ScoreCommand{Command: c, Score: score})
			return err
		}, id, a.Reason); err != nil {
			return err
		}
		rec.ScoreVersion++
		state.Events[target] = rec
	}
	return nil
}

// versioned supplies the current aggregate version and retries one CAS
// conflict. Idempotency keys make a replay after an unknown outcome safe.
func (w *JudgmentWorker) versioned(ctx context.Context, fn func(dto.Command) error, key, reason string) error {
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		var version int
		if version, err = w.Framework.Version(ctx, ""); err != nil {
			return err
		}
		err = fn(dto.Command{SchemaVersion: "strategy-2.0", RequestID: key, IdempotencyKey: key, ExpectedVersion: version, Reason: reason})
		var known *d.Error
		if err == nil || !errors.As(err, &known) || known.Code != "FRAMEWORK_AGGREGATE_VERSION_CONFLICT" {
			return err
		}
	}
	return err
}

func (w *JudgmentWorker) inputs(ctx context.Context, now time.Time) ([]d.NewsItem, []d.NewsItem, bool, error) {
	s := w.Settings
	rows, err := w.Store.Records(ctx, "NEWS", now.Add(-s.OlderWindow), now.Add(time.Second), 5000)
	if err != nil {
		return nil, nil, false, err
	}
	recent, older := []d.NewsItem{}, []d.NewsItem{}
	for _, raw := range rows {
		var x d.NewsItem
		if json.Unmarshal(raw, &x) != nil || x.Recap || x.ReceivedAt.After(now) {
			continue
		}
		if now.Sub(x.ReceivedAt) <= s.RecentWindow {
			recent = append(recent, x)
		} else {
			older = append(older, x)
		}
	}
	if len(recent) > s.MaxRecent {
		recent = recent[len(recent)-s.MaxRecent:]
	}
	if len(older) > s.MaxOlder {
		older = older[len(older)-s.MaxOlder:]
	}
	sources := map[string]NewsSourceState{}
	if _, err = w.Store.LoadState(ctx, "NEWS_SOURCES", &sources); err != nil {
		return nil, nil, false, err
	}
	var last time.Time
	failing := false
	for _, v := range sources {
		if v.LastSuccess.After(last) {
			last = v.LastSuccess
		}
		failing = failing || v.Failing
	}
	return recent, older, d.NewsFresh(last, failing, len(recent), now, s.MaxNewsAge), nil
}

func (w *JudgmentWorker) latestJudgment(ctx context.Context, now time.Time) (*d.Judgment, error) {
	rows, err := w.Store.Records(ctx, "JUDGMENT", now.Add(-72*time.Hour), now.Add(time.Second), 200)
	if err != nil {
		return nil, err
	}
	for i := len(rows) - 1; i >= 0; i-- {
		var j d.Judgment
		if json.Unmarshal(rows[i], &j) == nil && j.OK {
			return &j, nil
		}
	}
	return nil, nil
}

func (w *JudgmentWorker) alert(ctx context.Context, code, detail string) {
	if w.Alerts != nil {
		w.Alerts.Alert(ctx, code, detail)
	}
}

func truncate(v string, n int) string {
	if r := []rune(v); len(r) > n {
		return string(r[:n])
	}
	return v
}
