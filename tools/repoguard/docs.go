package repoguard

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"unicode"
)

// DocumentPairs lists the specification/design pairs of the current baseline.
// Each pair is doc/<pair>式样书.md (what and why, acceptance) plus
// doc/<pair>设计书.md (how the code implements it).
var DocumentPairs = []string{"01_交易系统层", "02_策略化框架层", "03_SOXLUSDT应用实例", "04_Web管理台", "05_模型接入模块"}

// FieldCoverage names the configuration types whose every JSON/TOML field must
// be explained in the design book where the logic that uses it is described.
type FieldCoverage struct {
	Design string
	File   string
	Types  []string
	Tag    string
}

var versionLine = regexp.MustCompile(`(?m)^版本：(\d+\.\d+\.\d+)`)
var mdLink = regexp.MustCompile(`\]\(([^)\s]+)\)`)

// CheckDocuments validates the current Markdown baseline (CheckBooks) and the
// configuration field coverage of the design books. It returns the file count.
func CheckDocuments(root, version string) (int, error) {
	n, texts, err := CheckBooks(root, version)
	if err != nil {
		return 0, err
	}
	if err := CheckFieldCoverage(root, texts); err != nil {
		return 0, err
	}
	return n, nil
}

// CheckBooks validates README and every pair: version header, relative links
// and anchors, and no executable content. It returns the file count and texts.
func CheckBooks(root, version string) (int, map[string]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, "doc"))
	if err != nil {
		return 0, nil, err
	}
	for _, e := range entries {
		if e.IsDir() && regexp.MustCompile(`^v\d`).MatchString(e.Name()) {
			return 0, nil, fmt.Errorf("doc/%s: earlier baselines live in Git tags, not in the tree", e.Name())
		}
	}
	names := []string{"doc/README.md"}
	for _, pair := range DocumentPairs {
		names = append(names, "doc/"+pair+"式样书.md", "doc/"+pair+"设计书.md")
	}
	texts := map[string]string{}
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return 0, nil, fmt.Errorf("%s: missing", name)
		}
		text := string(data)
		texts[name] = text
		if m := versionLine.FindStringSubmatch(text); m == nil || m[1] != version {
			return 0, nil, fmt.Errorf("%s: header must read 版本：%s", name, version)
		}
		if regexp.MustCompile(`(?i)<script|<iframe|javascript:`).MatchString(text) {
			return 0, nil, fmt.Errorf("%s: executable content", name)
		}
	}
	for _, name := range names {
		if err := checkMarkdownLinks(root, name, texts); err != nil {
			return 0, nil, err
		}
	}
	return len(names), texts, nil
}

// SpecSemantics normalizes a specification for the design-release rule: the
// version header and whitespace do not count as a requirement change.
func SpecSemantics(text string) string {
	text = versionLine.ReplaceAllString(text, "")
	return strings.Join(strings.Fields(text), " ")
}

// Slug follows GitHub's heading anchor rule: lower-case, punctuation removed
// (letters, digits, CJK, '-' and '_' kept), spaces turned into '-'.
func Slug(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(heading)) {
		switch {
		case r == ' ':
			b.WriteRune('-')
		case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

func anchors(text string) map[string]bool {
	out := map[string]bool{}
	seen := map[string]int{}
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence || !strings.HasPrefix(line, "#") {
			continue
		}
		heading := strings.TrimLeft(line, "#")
		heading = regexp.MustCompile("`|\\*\\*").ReplaceAllString(heading, "")
		s := Slug(heading)
		if n := seen[s]; n > 0 {
			out[fmt.Sprintf("%s-%d", s, n)] = true
		} else {
			out[s] = true
		}
		seen[s]++
	}
	return out
}

func checkMarkdownLinks(root, name string, texts map[string]string) error {
	for _, m := range mdLink.FindAllStringSubmatch(texts[name], -1) {
		target := m[1]
		if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
			continue
		}
		file, anchor, _ := strings.Cut(target, "#")
		resolved := name
		if file != "" {
			resolved = filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(name), file)))
			if _, err := os.Stat(filepath.Join(root, resolved)); err != nil {
				return fmt.Errorf("%s: broken link %s", name, target)
			}
		}
		if anchor == "" {
			continue
		}
		text, ok := texts[resolved]
		if !ok {
			data, err := os.ReadFile(filepath.Join(root, resolved))
			if err != nil {
				return fmt.Errorf("%s: broken link %s", name, target)
			}
			text = string(data)
		}
		if !anchors(text)[anchor] {
			return fmt.Errorf("%s: missing anchor %s", name, target)
		}
	}
	return nil
}

// ConfigFields returns the tagged field names of the named struct types in a
// Go file (tag key "json" or "toml"); "-" and empty names are skipped.
func ConfigFields(path string, types []string, key string) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	for _, t := range types {
		wanted[t] = true
	}
	var out []string
	found := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.TypeSpec)
		if !ok || !wanted[spec.Name.Name] {
			return true
		}
		st, ok := spec.Type.(*ast.StructType)
		if !ok {
			return true
		}
		found[spec.Name.Name] = true
		var walk func(*ast.StructType)
		walk = func(st *ast.StructType) {
			for _, f := range st.Fields.List {
				if f.Tag != nil {
					tag := reflect.StructTag(strings.Trim(f.Tag.Value, "`")).Get(key)
					name, _, _ := strings.Cut(tag, ",")
					if name != "" && name != "-" {
						out = append(out, name)
					}
				}
				if inner, ok := f.Type.(*ast.StructType); ok {
					walk(inner)
				}
			}
		}
		walk(st)
		return false
	})
	for _, t := range types {
		if !found[t] {
			return nil, fmt.Errorf("%s: type %s not found", path, t)
		}
	}
	return out, nil
}

// CheckFieldCoverage requires every configuration field listed in Coverage to
// appear as `field` in its design book.
func CheckFieldCoverage(root string, texts map[string]string) error {
	for _, c := range Coverage {
		fields, err := ConfigFields(filepath.Join(root, c.File), c.Types, c.Tag)
		if err != nil {
			return err
		}
		text := texts["doc/"+c.Design]
		var missing []string
		for _, f := range fields {
			if !strings.Contains(text, "`"+f+"`") {
				missing = append(missing, f)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("doc/%s: configuration fields not explained: %s", c.Design, strings.Join(missing, ", "))
		}
	}
	return nil
}
