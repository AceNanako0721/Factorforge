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

type Release struct {
	Version   string   `json:"version"`
	Directory string   `json:"directory"`
	Kind      string   `json:"change_kind"`
	Date      string   `json:"date"`
	Reason    string   `json:"reason"`
	Format    string   `json:"format"`
	Pairs     []string `json:"document_pairs,omitempty"`
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
func CheckDirectories(root string, records []Release) error {
	registered := map[string]bool{"doc/v0.1": true, "doc/v1.0": true, "doc/v1.1": true}
	for _, r := range records {
		registered[r.Directory] = true
	}
	entries, err := os.ReadDir(filepath.Join(root, "doc"))
	if err != nil {
		return err
	}
	pattern := regexp.MustCompile(`^v\d+(?:\.\d+)+$`)
	for _, entry := range entries {
		if entry.IsDir() && pattern.MatchString(entry.Name()) && !registered["doc/"+entry.Name()] {
			return fmt.Errorf("unregistered version directory")
		}
	}
	return nil
}
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
	if len(manifest.Releases) < len(old.Releases) || !equalJSON(manifest.Releases[:len(old.Releases)], old.Releases) {
		return fmt.Errorf("published release records cannot be overwritten")
	}
	if kind == "code-only" && len(manifest.Releases) != len(old.Releases) {
		return fmt.Errorf("code-only cannot append a release")
	}
	if kind != "code-only" && (len(manifest.Releases) != len(old.Releases)+1 || manifest.Releases[len(manifest.Releases)-1].Kind != kind) {
		return fmt.Errorf("document change requires matching appended release")
	}
	frozen := []string{"doc/v0.1", "doc/v1.0", "doc/v1.1"}
	for _, r := range old.Releases {
		frozen = append(frozen, r.Directory)
	}
	raw, err = Git(root, "diff", "--name-only", "-z", base, "--")
	if err != nil {
		return err
	}
	for _, name := range strings.Split(string(raw), "\x00") {
		for _, dir := range frozen {
			if strings.HasPrefix(name, dir+"/") {
				return fmt.Errorf("published document baseline is immutable")
			}
		}
	}
	if kind == "design" {
		oldDir, newDir := old.Releases[len(old.Releases)-1].Directory, manifest.Releases[len(manifest.Releases)-1].Directory
		raw, err = Git(root, "ls-tree", "-r", "--name-only", "-z", base, oldDir)
		if err != nil {
			return err
		}
		names := map[string]bool{}
		for _, name := range strings.Split(string(raw), "\x00") {
			if !strings.HasSuffix(name, "式样书.md") && !strings.HasSuffix(name, "式样书.html") {
				continue
			}
			stem := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
			names[stem] = true
			fresh := filepath.Join(root, newDir, filepath.Base(name))
			data, err := os.ReadFile(fresh)
			if os.IsNotExist(err) {
				fresh = strings.TrimSuffix(fresh, filepath.Ext(fresh)) + ".html"
				data, err = os.ReadFile(fresh)
			}
			if err != nil {
				return fmt.Errorf("design release removes a specification")
			}
			before, err := Git(root, "show", base+":"+name)
			if err != nil {
				return err
			}
			if SpecSemantics(string(before), filepath.Ext(name)) != SpecSemantics(string(data), filepath.Ext(fresh)) {
				return fmt.Errorf("design release changes specification")
			}
		}
		entries, err := os.ReadDir(filepath.Join(root, newDir))
		if err != nil {
			return err
		}
		count := 0
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), "式样书.md") || strings.HasSuffix(entry.Name(), "式样书.html") {
				count++
				if !names[strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))] {
					return fmt.Errorf("design release adds a specification")
				}
			}
		}
		if count != len(names) {
			return fmt.Errorf("design release removes a specification")
		}
	}
	return nil
}
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
	if json.Unmarshal(data, &manifest) != nil || manifest.Schema != 1 || len(manifest.Releases) == 0 || manifest.Releases[len(manifest.Releases)-1].Version != current {
		return "", fmt.Errorf("release manifest must match VERSION")
	}
	seen := map[string]bool{}
	for index, r := range manifest.Releases {
		if _, err = Version(r.Version); err != nil {
			return "", err
		}
		if seen[r.Version] || r.Kind == "code-only" {
			return "", fmt.Errorf("invalid or duplicate document release")
		}
		if _, err = ExpectedVersion("0.0.0", r.Kind); err != nil {
			return "", err
		}
		seen[r.Version] = true
		if !(index == 0 && r.Version == "2.0.0" && r.Directory == "doc/v2.0") && r.Directory != "doc/v"+r.Version {
			return "", fmt.Errorf("new baseline requires three-part directory")
		}
		info, err := os.Stat(filepath.Join(root, r.Directory))
		if err != nil || !info.IsDir() || r.Reason == "" || r.Date == "" {
			return "", fmt.Errorf("release directory, reason and date required")
		}
	}
	if err = CheckDirectories(root, manifest.Releases); err != nil {
		return "", err
	}
	last := manifest.Releases[len(manifest.Releases)-1]
	if versionAtLeast(current, "2.1.0") {
		if last.Format != "html" || len(last.Pairs) == 0 {
			return "", fmt.Errorf("current release must declare HTML pairs")
		}
		for _, prefix := range []string{"01_交易系统层", "02_策略化框架层", "03_SOXLUSDT_JEV应用实例"} {
			found := false
			for _, p := range last.Pairs {
				found = found || prefix == p
			}
			if !found {
				return "", fmt.Errorf("original layer pairs must remain")
			}
		}
		if _, err = CheckHTML(root, last.Directory, current, last.Pairs); err != nil {
			return "", err
		}
		if _, err = os.Stat(filepath.Join(root, last.Directory, "开发规划书.html")); err != nil {
			return "", fmt.Errorf("phase plan missing")
		}
	} else {
		if err = CheckLegacyWords(root, last.Directory); err != nil {
			return "", err
		}
	}
	if base != "" {
		if err = CheckTransition(root, base, kind, current, manifest); err != nil {
			return "", err
		}
	}
	return current, nil
}
