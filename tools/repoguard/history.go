package repoguard

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func readText(root, name string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	return strings.ReplaceAll(string(data), "\r\n", "\n"), err
}
func matches(pattern, text string) []string {
	values := []string{}
	for _, match := range regexp.MustCompile(pattern).FindAllStringSubmatch(text, -1) {
		values = append(values, match[1])
	}
	return values
}
func set(values []string) map[string]bool {
	out := map[string]bool{}
	for _, v := range values {
		out[v] = true
	}
	return out
}
func checkLinks(root, name, text string) error {
	for _, link := range matches(`\]\(([^)]+)\)`, text) {
		if strings.HasPrefix(link, "https://") || strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "#") {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, filepath.Dir(filepath.FromSlash(name)), filepath.FromSlash(link))); err != nil {
			return fmt.Errorf("historical local link broken: %s", name)
		}
	}
	return nil
}
func digest(data []byte) string { hash := sha256.Sum256(data); return hex.EncodeToString(hash[:]) }
func CheckHistory(root string) error {
	design, err := readText(root, "doc/v1.1/详细设计书_v1.1.md")
	if err != nil {
		return err
	}
	start, end := strings.Index(design, "<!-- TRACE_MATRIX_START -->"), strings.Index(design, "<!-- TRACE_MATRIX_END -->")
	if start < 0 || end < start {
		return fmt.Errorf("historical trace matrix missing")
	}
	matrix := design[start:end]
	sources := map[string][2]string{"REQ": {"要求分析式样书_v1.1.md", `(REQ-[A-Z]+-\d{3})`}, "NFR": {"要求分析式样书_v1.1.md", `(NFR-\d{3})`}, "CR": {"SOXLUSDT实盘系统完整开发规划书_v1.1.md", `(CR-\d{2})`}, "DEC": {"要求分析式样书_v1.1.md", `(DEC-\d{2})`}, "WP": {"SOXLUSDT实盘系统完整开发规划书_v1.1.md", `(WP-\d{2})`}, "AC": {"要求分析式样书_v1.1.md", `(?:^|[^A-Za-z0-9])(AC-\d{2})(?:$|[^\d])`}, "TC": {"SOXLUSDT实盘系统完整开发规划书_v1.1.md", `(?:^|[^A-Za-z0-9])(TC-\d{2})(?:$|[^\d])`}}
	for kind, source := range sources {
		raw, err := os.ReadFile(filepath.Join(root, "doc/v1.1", source[0]))
		if err != nil {
			return err
		}
		ids := matches(`(?m)^\| (`+kind+`-[A-Z]+-\d{3}|`+kind+`-\d{2,3}) \|`, matrix)
		if len(ids) != len(set(ids)) || !equalJSON(set(ids), set(matches(source[1], string(raw)))) || !strings.Contains(design, digest(raw)) {
			return fmt.Errorf("historical IDs or source hash drift: %s", kind)
		}
	}
	adrs := set(matches(`(?m)^\| (ADR-\d{3}) \|`, design))
	for _, line := range strings.Split(matrix, "\n") {
		if regexp.MustCompile(`^\| (REQ|NFR|CR|DEC|WP)-`).MatchString(line) {
			for _, id := range matches(`(ADR-\d{3})`, line) {
				if !adrs[id] {
					return fmt.Errorf("unregistered historical ADR")
				}
			}
		}
	}
	if err = checkLinks(root, "doc/v1.1/详细设计书_v1.1.md", design); err != nil {
		return err
	}
	if strings.Contains(design, "write:analysis") || !strings.Contains(design, "ports/EvidenceExtractor") || !strings.Contains(design, "ADR-017") || !strings.Contains(design, "ADR-019") || !strings.Contains(design, "../../contracts/openapi/v1.json") || len(matches("(?m)^(```)", design))%2 != 0 {
		return fmt.Errorf("historical architecture or diagram drift")
	}
	known := map[string]bool{}
	requirementCount := 0
	for layer, prefix := range []string{"01_交易系统层", "02_策略化框架层", "03_SOXLUSDT_JEV应用实例"} {
		n := layer + 1
		spec, err := readText(root, "doc/v2.0/"+prefix+"式样书.md")
		if err != nil {
			return err
		}
		implementation, err := readText(root, "doc/v2.0/"+prefix+"设计书.md")
		if err != nil {
			return err
		}
		req := matches(fmt.Sprintf(`(?m)^### (S%d-\d{3}) `, n), spec)
		tests := matches(fmt.Sprintf(`(?m)^\| (T%d-\d{2}) \|`, n), spec)
		rows := matches(fmt.Sprintf(`(?m)^\| (S%d-\d{3}) \|`, n), implementation)
		if len(req) != len(set(req)) || len(rows) != len(set(rows)) || !equalJSON(set(rows), set(req)) || len(tests) == 0 || len(tests) != len(set(tests)) {
			return fmt.Errorf("historical layer mappings incomplete")
		}
		requirementCount += len(req)
		for _, id := range append(req, tests...) {
			known[id] = true
		}
		for _, line := range strings.Split(implementation, "\n") {
			if regexp.MustCompile(fmt.Sprintf(`^\| S%d-\d{3} \|`, n)).MatchString(line) {
				refs := matches(fmt.Sprintf(`(T%d-\d{2})`, n), line)
				if len(refs) == 0 {
					return fmt.Errorf("historical requirement lacks test")
				}
				for _, ref := range refs {
					if !set(tests)[ref] {
						return fmt.Errorf("historical requirement test unknown")
					}
				}
			}
		}
		if regexp.MustCompile("```|CREATE TABLE|/api/v2|POST /|class \\w+").MatchString(spec) || n < 3 && regexp.MustCompile(`SOXLUSDT|\bJEV\b|\bJev\b|typesafe`).MatchString(spec+implementation) {
			return fmt.Errorf("historical layer boundaries changed")
		}
	}
	for i := 1; i <= 5; i++ {
		known[fmt.Sprintf("P%d", i)] = true
	}
	trace, err := readText(root, "doc/v2.0/v1.1需求迁移与待决事项.md")
	if err != nil {
		return err
	}
	req, err := readText(root, "doc/v1.1/要求分析式样书_v1.1.md")
	if err != nil {
		return err
	}
	oldPlan, err := readText(root, "doc/v1.1/SOXLUSDT实盘系统完整开发规划书_v1.1.md")
	if err != nil {
		return err
	}
	expected := set(matches(`(?m)^(REQ-[A-Z]+-\d{3}|NFR-\d{3})`, req))
	for _, id := range matches(`(?:^|[^A-Za-z0-9])((?:CR|DEC|WP|AC|TC)-\d{2})(?:$|[^\d])`, req+oldPlan) {
		expected[id] = true
	}
	seen := map[string]bool{}
	for _, row := range regexp.MustCompile(`(?m)^\| ((?:REQ-[A-Z]+-\d{3}|NFR-\d{3}|(?:CR|DEC|WP|AC|TC)-\d{2})) \| ([^\n]+) \|$`).FindAllStringSubmatch(trace, -1) {
		if !regexp.MustCompile(`^[STP0-9\- ]+$`).MatchString(row[2]) {
			continue
		}
		if seen[row[1]] {
			return fmt.Errorf("duplicate historical migration ID")
		}
		seen[row[1]] = true
		for _, id := range strings.Fields(row[2]) {
			if !known[id] {
				return fmt.Errorf("unknown historical migration target")
			}
		}
	}
	if !equalJSON(expected, seen) || len(seen) != 200 || requirementCount != 55 {
		return fmt.Errorf("historical migration matrix incomplete: expected=%d seen=%d requirements=%d", len(expected), len(seen), requirementCount)
	}
	for i := 1; i <= 3; i++ {
		if !strings.Contains(trace, fmt.Sprintf("OD-%02d", i)) {
			return fmt.Errorf("historical unresolved item missing")
		}
	}
	plan, err := readText(root, "doc/v2.0/开发规划书.md")
	if err != nil {
		return err
	}
	if len(strings.Split(strings.TrimSuffix(plan, "\n"), "\n")) > 40 || len([]byte(plan)) >= 3000 || len(matches(`(?m)^\| (P\d) `, plan)) != 5 {
		return fmt.Errorf("historical phase plan no longer concise")
	}
	paths, _ := filepath.Glob(filepath.Join(root, "doc/v2.0/*.md"))
	paths = append(paths, filepath.Join(root, "doc/README.md"))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		name, _ := filepath.Rel(root, path)
		if strings.ContainsRune(string(data), '\ufffd') {
			return fmt.Errorf("historical document encoding corrupted")
		}
		if err = checkLinks(root, name, string(data)); err != nil {
			return err
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "doc/版本归档清单.json"))
	if err != nil {
		return err
	}
	var archive struct {
		Items []struct {
			From     string `json:"from"`
			To       string `json:"to"`
			Original string `json:"original_sha256"`
			Archived string `json:"archived_sha256"`
			Links    []any  `json:"link_changes"`
		} `json:"items"`
	}
	if json.Unmarshal(data, &archive) != nil {
		return fmt.Errorf("invalid archive manifest")
	}
	for _, item := range archive.Items {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(item.To)))
		if err != nil || digest(data) != item.Archived {
			return fmt.Errorf("archived document hash drift")
		}
		if len(item.Links) > 0 {
			original, err := os.ReadFile(filepath.Join(root, filepath.Dir(filepath.FromSlash(item.To)), ".originals", filepath.Base(item.To)))
			if err != nil || digest(original) != item.Original {
				return fmt.Errorf("original archived source hash drift")
			}
			updated := regexp.MustCompile(`\]\((\.\./(?:contracts|\.github)/[^)]+)\)`).ReplaceAllString(strings.ReplaceAll(string(original), "\r\n", "\n"), "](../$1)")
			if updated != strings.ReplaceAll(string(data), "\r\n", "\n") {
				return fmt.Errorf("archived link rebasing drift")
			}
		} else if item.Original != item.Archived {
			return fmt.Errorf("archive body changed")
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(item.From))); !os.IsNotExist(err) {
			return fmt.Errorf("historical root document remains")
		}
	}
	return CheckLegacyWords(root, "doc/v2.0")
}
