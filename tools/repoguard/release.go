package repoguard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Release is one published document version. Earlier versions are reachable
// through their Git tags; only the current baseline lives in doc/.
type Release struct {
	Version string `json:"version"`
	Kind    string `json:"change_kind"`
	Date    string `json:"date"`
	Summary string `json:"summary,omitempty"`
}
type Manifest struct {
	Schema   int       `json:"schema_version"`
	Releases []Release `json:"releases"`
}

func readJSON(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	err = json.Unmarshal(data, &value)
	return value, err
}
func Version(value string) ([3]int, error) {
	var v [3]int
	if !regexp.MustCompile(`^(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)$`).MatchString(value) {
		return v, fmt.Errorf("VERSION requires three numeric components")
	}
	for i, s := range strings.Split(value, ".") {
		n, e := strconv.Atoi(s)
		if e != nil {
			return v, fmt.Errorf("version component out of range")
		}
		v[i] = n
	}
	return v, nil
}
func versionAtLeast(a, b string) bool {
	x, _ := Version(a)
	y, _ := Version(b)
	for i := 0; i < 3; i++ {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return true
}
func ExpectedVersion(old, kind string) (string, error) {
	v, err := Version(old)
	if err != nil {
		return "", err
	}
	switch kind {
	case "framework":
		v = [3]int{v[0] + 1, 0, 0}
	case "specification":
		v = [3]int{v[0], v[1] + 1, 0}
	case "design":
		v[2]++
	case "code-only":
	default:
		return "", fmt.Errorf("unknown Change-Type")
	}
	return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]), nil
}
func ParseKind(body string) (string, error) {
	body = regexp.MustCompile(`(?s)<!--.*?-->`).ReplaceAllString(body, "")
	rows := regexp.MustCompile(`(?m)^Change-Type:\s*([\w-]+)\s*$`).FindAllStringSubmatch(body, -1)
	if len(rows) != 1 {
		return "", fmt.Errorf("PR requires one Change-Type")
	}
	kind := rows[0][1]
	if _, err := ExpectedVersion("0.0.0", kind); err != nil {
		return "", err
	}
	sections := map[string]string{}
	heading := ""
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "## ") {
			heading = strings.TrimSpace(line)
		} else if heading != "" {
			sections[heading] += line + "\n"
		}
	}
	for _, key := range []string{"## 变更内容", "## 验证"} {
		if strings.TrimSpace(sections[key]) == "" {
			return "", fmt.Errorf("PR change description and validation required")
		}
	}
	return kind, nil
}

// CheckTransition validates a pull request against its base: the version
// increment matches the Change-Type, published release records are kept, and a
// design change leaves every specification's requirements untouched.
func CheckTransition(root, base, kind, current string, manifest Manifest) error {
	raw, err := Git(root, "show", base+":VERSION")
	if err != nil {
		return err
	}
	expected, err := ExpectedVersion(strings.TrimSpace(string(raw)), kind)
	if err != nil || current != expected {
		return fmt.Errorf("incorrect version increment for %s", kind)
	}
	raw, err = Git(root, "show", base+":doc/releases.json")
	if err != nil {
		return err
	}
	var old Manifest
	if json.Unmarshal(raw, &old) != nil {
		return fmt.Errorf("invalid base release manifest")
	}
	if len(manifest.Releases) < len(old.Releases) {
		return fmt.Errorf("published release records cannot be removed")
	}
	for i, r := range old.Releases {
		n := manifest.Releases[i]
		if n.Version != r.Version || n.Kind != r.Kind || n.Date != r.Date || (old.Schema == 2 && n.Summary != r.Summary) {
			return fmt.Errorf("published release records cannot be overwritten")
		}
	}
	if kind == "code-only" && len(manifest.Releases) != len(old.Releases) {
		return fmt.Errorf("code-only cannot append a release")
	}
	if kind != "code-only" && (len(manifest.Releases) != len(old.Releases)+1 || manifest.Releases[len(manifest.Releases)-1].Kind != kind) {
		return fmt.Errorf("document change requires matching appended release")
	}
	if kind != "design" || old.Schema != 2 {
		return nil
	}
	for _, pair := range DocumentPairs {
		name := "doc/" + pair + "式样书.md"
		before, err := Git(root, "show", base+":"+name)
		if err != nil {
			return err
		}
		after, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return fmt.Errorf("design release removes a specification")
		}
		if SpecSemantics(string(before)) != SpecSemantics(string(after)) {
			return fmt.Errorf("design release changes specification %s", pair)
		}
	}
	return nil
}

// CheckRepository validates VERSION, the release manifest and the current
// document baseline; with a base commit it also validates the transition.
func CheckRepository(root, base, kind string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		return "", err
	}
	current := strings.TrimSpace(string(data))
	if _, err = Version(current); err != nil {
		return "", err
	}
	data, err = os.ReadFile(filepath.Join(root, "doc/releases.json"))
	if err != nil {
		return "", err
	}
	var manifest Manifest
	if json.Unmarshal(data, &manifest) != nil || manifest.Schema != 2 || len(manifest.Releases) == 0 || manifest.Releases[len(manifest.Releases)-1].Version != current {
		return "", fmt.Errorf("release manifest must match VERSION")
	}
	previous := "0.0.0"
	for _, r := range manifest.Releases {
		if _, err = Version(r.Version); err != nil {
			return "", err
		}
		if r.Kind == "code-only" || r.Date == "" || versionAtLeast(previous, r.Version) {
			return "", fmt.Errorf("release records must be ordered, dated document releases")
		}
		if _, err = ExpectedVersion("0.0.0", r.Kind); err != nil {
			return "", err
		}
		previous = r.Version
	}
	if strings.TrimSpace(manifest.Releases[len(manifest.Releases)-1].Summary) == "" {
		return "", fmt.Errorf("current release requires a summary")
	}
	if _, err = CheckDocuments(root, current); err != nil {
		return "", err
	}
	if base != "" {
		if err = CheckTransition(root, base, kind, current, manifest); err != nil {
			return "", err
		}
	}
	return current, nil
}
