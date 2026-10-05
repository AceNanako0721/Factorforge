// Package layoutguard checks source placement and Go imports without executing
// source, loading private assets, or relying on Python.
package layoutguard

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const module = "github.com/AceNanako0721/Factorforge/src/factorforge/"

func Check(root string) ([]string, error) {
	failures := []string{}
	err := filepath.WalkDir(filepath.Join(root, "src"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(relative)
		parts := strings.Split(name, "/")
		fail := func(reason string) { failures = append(failures, name+": "+reason) }
		if len(parts) < 5 || parts[1] != "factorforge" {
			fail("Go source outside registered layer area")
			return nil
		}
		layer, area := parts[2], parts[3]
		if layer != "trading" && layer != "strategy" && layer != "applications" {
			fail("unregistered layer")
			return nil
		}
		if strings.HasSuffix(name, "_test.go") {
			fail("tests belong under tests")
			return nil
		}
		if layer != "applications" && !has([]string{"domain", "application", "ports", "adapters", "api", "workers", "entrypoints"}, area) {
			fail("unregistered layer area")
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if layer == "trading" && strings.HasPrefix(imported, module) && !strings.HasPrefix(imported, module+"trading/") {
				fail("upward layer import")
			}
			if layer == "strategy" && strings.HasPrefix(imported, module+"applications/") {
				fail("upward layer import")
			}
			if layer == "strategy" && strings.HasPrefix(imported, module+"trading/") && !prefix(imported, module+"trading/api/", []string{"dto", "views"}) {
				fail("strategy requires public trading DTO/client")
			}
			own := module + layer + "/"
			if area == "domain" && strings.HasPrefix(imported, own) && !strings.HasPrefix(imported, own+"domain") {
				fail("domain depends on outer package")
			}
			if area == "application" && prefix(imported, own, []string{"api", "workers", "adapters", "entrypoints"}) {
				fail("reversed application dependency")
			}
			if (area == "ports" || area == "adapters") && prefix(imported, own, []string{"application", "api", "workers", "entrypoints"}) {
				fail("adapter/port depends on application entry")
			}
			if (area == "domain" || area == "ports") && (imported == "net" || strings.HasPrefix(imported, "net/") || imported == "os/exec" || strings.HasPrefix(imported, "github.com/jackc/pgx")) {
				fail("infrastructure in domain/port")
			}
			if strings.HasPrefix(imported, "github.com/AceNanako0721/Factorforge/tools/") {
				fail("production imports repository tool")
			}
		}
		return nil
	})
	sort.Strings(failures)
	return failures, err
}
func has(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func prefix(value, base string, areas []string) bool {
	for _, area := range areas {
		if value == base+area || strings.HasPrefix(value, base+area+"/") {
			return true
		}
	}
	return false
}
