package repoguard

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/pelletier/go-toml/v2"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
)

var templates = map[string]bool{"config/config.example.toml": true, "prompts/prompts.example.json": true}
var patterns = map[string]*regexp.Regexp{
	"github-token":              regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{30,}\b`),
	"github-fine-grained-token": regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{40,}\b`),
	"aws-access-key":            regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`),
	"slack-token":               regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{20,}\b`),
	"private-key":               regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----`),
	"url-credentials":           regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://[^\s/:@]+:[^\s/@]+@`),
	"credential-assignment":     regexp.MustCompile(`(?i)\b(?:[A-Za-z0-9]+_)*(?:api[_-]?key|api[_-]?secret|secret[_-]?key|cursor[_-]?key|access[_-]?token|auth[_-]?token|password)\b["']?\s*[:=]\s*["'][A-Za-z0-9_+./=-]{16,}["']`),
}

type Issue struct{ Path, Rule string }

func PathIssue(name string) string {
	parts := strings.Split(name, "/")
	if (parts[0] == "config" || parts[0] == "prompts") && !templates[name] {
		return "private-asset-path"
	}
	private := map[string]bool{"private": true, ".private": true, "secrets": true, "model_traces": true, "ai_cache": true, "runtime": true, "logs": true, "backups": true, ".venv": true, "__pycache__": true}
	for _, part := range parts {
		if private[part] {
			return "private-runtime-path"
		}
		if part == ".env" || strings.HasPrefix(part, ".env.") {
			return "private-environment-file"
		}
	}
	for _, prefix := range []string{"data/raw/", "data/private/", "doc/_archive/"} {
		if strings.HasPrefix(name, prefix) {
			return "private-data-path"
		}
	}
	for _, ext := range []string{".pem", ".key", ".p12", ".pfx", ".db"} {
		if strings.EqualFold(path.Ext(name), ext) {
			return "private-key-or-database"
		}
	}
	if regexp.MustCompile(`^doc/\.[^/]+-build/`).MatchString(name) && path.Ext(name) != ".py" && path.Ext(name) != ".ps1" {
		return "private-document-intermediate"
	}
	return ""
}
func equalJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}
func cloneMap(value map[string]any) map[string]any {
	data, _ := json.Marshal(value)
	out := map[string]any{}
	json.Unmarshal(data, &out)
	return out
}
func asMap(value any) map[string]any {
	if x, ok := value.(map[string]any); ok {
		return x
	}
	return map[string]any{}
}
func TemplateIssue(name string, data []byte, historical bool) string {
	var actual, expected map[string]any
	if name == "config/config.example.toml" {
		if toml.Unmarshal(data, &actual) != nil || toml.Unmarshal([]byte(expectedConfig), &expected) != nil {
			return "invalid-template-format"
		}
		// Enforce a complete, exact empty shape, including short arbitrary secrets.
		if actual["mode"] != "mock" || !equalJSON(actual["schema_version"], expected["schema_version"]) || asMap(actual["runtime"])["environment"] != "SIM" || asMap(actual["trading"])["allow_live"] != false || asMap(actual["trading"])["adapter"] != "mock" {
			return "unsafe-config-template-defaults"
		}
		if !equalJSON(actual["credentials"], expected["credentials"]) {
			return "nonempty-or-invalid-template-credentials"
		}
		services := expected["services"]
		oldServices := cloneMap(asMap(services))
		delete(oldServices, "execution_database_url")
		if !equalJSON(actual["services"], services) && !(historical && equalJSON(actual["services"], oldServices)) {
			return "nonempty-template-service-settings"
		}
		if equalJSON(actual, expected) {
			return ""
		}
		if historical {
			old := cloneMap(expected)
			application := cloneMap(asMap(old["application"]))
			delete(application, "pipeline")
			old["application"] = application
			old["services"] = actual["services"]
			if equalJSON(actual, old) {
				return ""
			}
			delete(application, "read_api")
			old["application"] = application
			old["services"] = actual["services"]
			if equalJSON(actual, old) {
				return ""
			}
			strategy := asMap(old["strategy"])
			for _, key := range []string{"query_default_limit", "query_max_limit", "query_max_records", "query_cursor_age_seconds", "query_cursor_key"} {
				delete(strategy, key)
			}
			if _, exists := actual["strategy"]; !exists {
				delete(old, "strategy")
			}
			if equalJSON(actual, old) {
				return ""
			}
			for _, legacy := range []map[string]any{{"adapter": "mock", "allow_live": false}, {"adapter": "mock", "allow_live": false, "account_id": "", "principal_id": "", "permissions": []any{}}} {
				older := cloneMap(old)
				older["trading"] = legacy
				if equalJSON(actual, older) {
					return ""
				}
			}
		}
		return "unrecognized-template-field"
	}
	if json.Unmarshal(data, &actual) != nil || json.Unmarshal([]byte(expectedPrompts), &expected) != nil {
		return "invalid-template-format"
	}
	if equalJSON(actual, expected) {
		return ""
	}
	return "real-prompt-in-template"
}
func ContentIssues(name string, data []byte, historical bool) []string {
	issues := map[string]bool{}
	if templates[name] {
		if rule := TemplateIssue(name, data, historical); rule != "" {
			issues[rule] = true
		}
	}
	payloads := [][]byte{data}
	if bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			issues["unreadable-document-container"] = true
		} else {
			var size uint64
			for _, file := range archive.File {
				if file.UncompressedSize64 > 64<<20 || size > 64<<20-file.UncompressedSize64 {
					issues["document-scan-size-limit"] = true
					break
				}
				size += file.UncompressedSize64
				if file.Flags&1 != 0 {
					issues["encrypted-document"] = true
					break
				}
				if strings.HasSuffix(file.Name, ".xml") || strings.HasSuffix(file.Name, ".rels") || strings.HasSuffix(file.Name, ".txt") || strings.HasSuffix(file.Name, ".json") {
					reader, e := file.Open()
					if e != nil {
						issues["unreadable-document-container"] = true
						break
					}
					body, e := io.ReadAll(io.LimitReader(reader, 64<<20+1))
					reader.Close()
					if e != nil || len(body) > 64<<20 {
						issues["unreadable-document-container"] = true
						break
					}
					payloads = append(payloads, body)
				}
			}
		}
	}
	strip := regexp.MustCompile(`<[^>]+>`)
	for _, payload := range payloads {
		joined := strip.ReplaceAll(payload, nil)
		for rule, pattern := range patterns {
			if pattern.Match(payload) || pattern.Match(joined) {
				issues[rule] = true
			}
		}
	}
	result := []string{}
	for rule := range issues {
		result = append(result, rule)
	}
	sort.Strings(result)
	return result
}
func Scan(root, revision string, history bool) ([]Issue, int, error) {
	revisions := []string{revision}
	head := ""
	if history {
		raw, err := Git(root, "rev-parse", "HEAD")
		if err != nil {
			return nil, 0, err
		}
		head = strings.TrimSpace(string(raw))
		revisions = []string{head}
		raw, err = Git(root, "rev-list", "--all")
		if err != nil {
			return nil, 0, err
		}
		for _, ref := range strings.Fields(string(raw)) {
			if ref != head {
				revisions = append(revisions, ref)
			}
		}
	}
	checked := map[string]bool{}
	found := map[string]bool{}
	issues := map[Issue]bool{}
	for _, ref := range revisions {
		args := []string{"ls-tree", "-r", "-z", ref}
		if ref == "" {
			args = []string{"ls-files", "--stage", "-z"}
		}
		raw, err := Git(root, args...)
		if err != nil {
			return nil, len(checked), err
		}
		for _, record := range bytes.Split(raw, []byte{0}) {
			if len(record) == 0 {
				continue
			}
			parts := bytes.SplitN(record, []byte{'\t'}, 2)
			if len(parts) != 2 {
				return nil, len(checked), fmt.Errorf("invalid Git object record")
			}
			metadata := strings.Fields(string(parts[0]))
			if len(metadata) != 3 {
				return nil, len(checked), fmt.Errorf("invalid Git object metadata")
			}
			mode, sha := metadata[0], metadata[2]
			if ref == "" {
				sha = metadata[1]
				if metadata[2] != "0" {
					return nil, len(checked), fmt.Errorf("unmerged Git index")
				}
			} else if metadata[1] != "blob" {
				return nil, len(checked), fmt.Errorf("embedded repository requires publication review")
			}
			name := string(parts[1])
			if templates[name] && (ref == head || !history) {
				found[name] = true
			}
			key := name + "\x00" + sha
			if checked[key] {
				continue
			}
			checked[key] = true
			if rule := PathIssue(name); rule != "" {
				issues[Issue{name, rule}] = true
				continue
			}
			if mode != "100644" && mode != "100755" {
				issues[Issue{name, "unsupported-link-or-file-mode"}] = true
				continue
			}
			data, err := Git(root, "cat-file", "blob", sha)
			if err != nil {
				return nil, len(checked), err
			}
			for _, rule := range ContentIssues(name, data, history && ref != head) {
				issues[Issue{name, rule}] = true
			}
		}
	}
	for name := range templates {
		if !found[name] {
			issues[Issue{"config/prompts", "required-public-template-missing"}] = true
		}
	}
	result := []Issue{}
	for issue := range issues {
		result = append(result, issue)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Path == result[j].Path {
			return result[i].Rule < result[j].Rule
		}
		return result[i].Path < result[j].Path
	})
	return result, len(checked), nil
}
