package repoguard

import (
	"fmt"
	"golang.org/x/net/html"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

type HTMLDocument struct {
	IDs               map[string]bool
	Links             []string
	Meta              map[string]string
	Body              string
	Articles, H1, SVG int
	Invalid           bool
}

func attribute(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}
func ParseHTML(text string) (HTMLDocument, error) {
	result := HTMLDocument{IDs: map[string]bool{}, Meta: map[string]string{}, Links: []string{}}
	node, err := html.Parse(strings.NewReader(text))
	if err != nil {
		return result, err
	}
	var body strings.Builder
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, inArticle bool) {
		inside := inArticle
		if n.Type == html.ElementNode {
			if n.Data == "article" {
				result.Articles++
				inside = true
			}
			if n.Data == "h1" {
				result.H1++
			}
			if id := attribute(n, "id"); id != "" {
				if result.IDs[id] {
					result.Invalid = true
				}
				result.IDs[id] = true
			}
			if n.Data == "meta" && strings.HasPrefix(attribute(n, "name"), "ff:") {
				if _, exists := result.Meta[attribute(n, "name")]; exists {
					result.Invalid = true
				}
				result.Meta[attribute(n, "name")] = attribute(n, "content")
			}
			if n.Data == "a" && attribute(n, "href") != "" {
				result.Links = append(result.Links, attribute(n, "href"))
			}
			if strings.Contains("|script|iframe|object|embed|form|", "|"+n.Data+"|") {
				result.Invalid = true
			}
			if strings.Contains("|img|link|video|audio|source|image|", "|"+n.Data+"|") && (attribute(n, "src") != "" || attribute(n, "href") != "") {
				result.Invalid = true
			}
			for _, a := range n.Attr {
				value := strings.ToLower(strings.TrimSpace(a.Val))
				if strings.HasPrefix(strings.ToLower(a.Key), "on") || strings.HasPrefix(value, "javascript:") || strings.HasPrefix(value, "data:text/html") || a.Key == "style" && regexp.MustCompile(`(?i)@import|url\s*\(`).MatchString(a.Val) {
					result.Invalid = true
				}
			}
			if n.Data == "style" {
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					if regexp.MustCompile(`(?i)@import|url\s*\(`).MatchString(c.Data) {
						result.Invalid = true
					}
				}
			}
			if n.Data == "svg" {
				result.SVG++
				title, desc := false, false
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					title = title || c.Type == html.ElementNode && c.Data == "title"
					desc = desc || c.Type == html.ElementNode && c.Data == "desc"
				}
				if attribute(n, "role") != "img" || attribute(n, "viewBox") == "" || !title || !desc {
					result.Invalid = true
				}
			}
		}
		if n.Type == html.TextNode && inside {
			body.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inside)
		}
		if inside && n.Type == html.ElementNode && strings.Contains("|p|li|td|th|h1|h2|h3|pre|", "|"+n.Data+"|") {
			body.WriteByte('\n')
		}
	}
	walk(node, false)
	result.Body = body.String()
	return result, nil
}
func SpecSemantics(text, suffix string) string {
	if suffix != ".html" {
		text = regexp.MustCompile(`(?m)^(?:版本[：:]|日期[：:]).*$`).ReplaceAllString(text, "")
	}
	if suffix == ".html" {
		doc, _ := ParseHTML(text)
		text = doc.Body
	} else {
		text = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`).ReplaceAllString(text, "$1")
		text = regexp.MustCompile(`(?m)^[#|\s:-]+`).ReplaceAllString(text, "")
		text = strings.NewReplacer("`", "", "**", "").Replace(text)
	}
	text = regexp.MustCompile(`版本[：:]\s*\d+(?:\.\d+)+[；;]\s*日期[：:]\s*[^。]+。`).ReplaceAllString(text, "")
	return regexp.MustCompile(`\s+`).ReplaceAllString(text, "")
}
func CheckHTML(root, directory, version string, pairs []string) (int, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return 0, err
	}
	base := filepath.Join(abs, filepath.FromSlash(directory))
	paths, err := filepath.Glob(filepath.Join(base, "*.html"))
	if err != nil || len(paths) == 0 {
		return 0, fmt.Errorf("HTML baseline index is missing")
	}
	if _, err = os.Stat(filepath.Join(base, "index.html")); err != nil {
		return 0, fmt.Errorf("HTML baseline index is missing")
	}
	for _, ext := range []string{"*.md", "*.docx"} {
		matches, _ := filepath.Glob(filepath.Join(base, ext))
		if len(matches) > 0 {
			return 0, fmt.Errorf("HTML baseline cannot contain companion copies")
		}
	}
	docs := map[string]HTMLDocument{}
	texts := map[string]string{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return 0, err
		}
		name := filepath.Base(path)
		text := string(data)
		doc, err := ParseHTML(text)
		if err != nil || !utf8.Valid(data) || !strings.HasPrefix(strings.ToLower(text), "<!doctype html>") || !strings.Contains(text, `<html lang="zh-CN"`) || !strings.Contains(text, `<meta charset="utf-8">`) || doc.Invalid || doc.Articles != 1 || doc.H1 != 1 {
			return 0, fmt.Errorf("invalid offline HTML structure: %s", name)
		}
		if version != "" && doc.Meta["ff:version"] != version {
			return 0, fmt.Errorf("incorrect HTML version: %s", name)
		}
		if !strings.Contains("|specification|design|plan|migration|index|", "|"+doc.Meta["ff:kind"]+"|") {
			return 0, fmt.Errorf("invalid HTML kind: %s", name)
		}
		docs[name] = doc
		texts[name] = text
	}
	specs := map[string]bool{}
	for name, doc := range docs {
		for _, link := range doc.Links {
			u, err := url.Parse(link)
			if err != nil {
				return 0, fmt.Errorf("invalid document link: %s", name)
			}
			if u.Scheme != "" || u.Host != "" {
				if u.Scheme != "http" && u.Scheme != "https" {
					return 0, fmt.Errorf("unsupported document link: %s", name)
				}
				continue
			}
			decoded, err := url.PathUnescape(u.Path)
			if err != nil {
				return 0, fmt.Errorf("invalid document link: %s", name)
			}
			target := filepath.Join(base, filepath.FromSlash(decoded))
			if decoded == "" {
				target = filepath.Join(base, name)
			}
			resolved, err := filepath.EvalSymlinks(target)
			if err != nil {
				return 0, fmt.Errorf("broken local document link: %s", name)
			}
			relative, err := filepath.Rel(abs, resolved)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return 0, fmt.Errorf("out-of-workspace document link: %s", name)
			}
			info, err := os.Stat(resolved)
			if err != nil || info.IsDir() {
				return 0, fmt.Errorf("broken local document link: %s", name)
			}
			if u.Fragment != "" && filepath.Ext(resolved) == ".html" {
				data, err := os.ReadFile(resolved)
				if err != nil {
					return 0, err
				}
				other, err := ParseHTML(string(data))
				if err != nil || !other.IDs[u.Fragment] {
					return 0, fmt.Errorf("broken HTML anchor: %s", name)
				}
			}
		}
		kind := doc.Meta["ff:kind"]
		if kind == "specification" || kind == "design" {
			pair, exists := docs[doc.Meta["ff:pair"]]
			opposite := "design"
			if kind == "design" {
				opposite = "specification"
			}
			if !exists || pair.Meta["ff:kind"] != opposite || pair.Meta["ff:pair"] != name {
				return 0, fmt.Errorf("HTML pair is not one-to-one: %s", name)
			}
			if kind == "specification" {
				specs[strings.TrimSuffix(name, "式样书.html")] = true
			}
		}
	}
	if len(pairs) > 0 {
		if len(specs) != len(pairs) {
			return 0, fmt.Errorf("HTML pairs disagree with release")
		}
		for _, name := range pairs {
			if !specs[name] {
				return 0, fmt.Errorf("HTML pair missing from release")
			}
		}
	}
	if version != "" && versionAtLeast(version, "2.1.0") {
		spec, design := texts["04_Web管理台式样书.html"], texts["04_Web管理台设计书.html"]
		for prefix, count := range map[string]int{"S4-": 16, "T4-": 14} {
			for i := 1; i <= count; i++ {
				width := 2
				if prefix == "S4-" {
					width = 3
				}
				id := fmt.Sprintf("%s%0*d", prefix, width, i)
				if !strings.Contains(spec, id) || !strings.Contains(design, id) {
					return 0, fmt.Errorf("frontend requirement mapping is missing: %s", id)
				}
			}
		}
		if docs["04_Web管理台式样书.html"].SVG < 3 || docs["04_Web管理台设计书.html"].SVG < 3 {
			return 0, fmt.Errorf("frontend program logic diagrams missing")
		}
		for _, contract := range []string{"contracts/v2/trading/openapi.json", "contracts/v2/strategy/openapi.json"} {
			value, err := readJSON(filepath.Join(abs, contract))
			if err != nil {
				return 0, err
			}
			for path, raw := range asMap(value["paths"]) {
				if _, ok := asMap(raw)["get"]; !ok {
					continue
				}
				suffix := path
				for _, prefix := range []string{"/api/v2/trading", "/api/v2/strategy"} {
					suffix = strings.TrimPrefix(suffix, prefix)
				}
				if !strings.Contains(design, suffix) && suffix != "/protections/{protection_id}" && suffix != "/market/trades" {
					return 0, fmt.Errorf("existing query absent from frontend design: %s", suffix)
				}
			}
		}
	}
	return len(paths), nil
}
