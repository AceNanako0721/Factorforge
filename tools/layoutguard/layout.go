// Package layoutguard checks source placement and Go imports against the layer
// registry (doc/layers.json) without executing source, loading private assets,
// or relying on Python.
package layoutguard

import (
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const module = "github.com/AceNanako0721/Factorforge/"

var standardAreas = []string{"domain", "application", "ports", "adapters", "api", "workers", "entrypoints"}

func Check(root string) ([]string, error) {
	registry, err := LoadRegistry(root)
	if err != nil {
		return nil, err
	}
	failures := registry.Validate()
	// Read only registered/unignored paths; ignored configuration and runtime
	// files are never inspected. Tiny non-Git import fixtures remain supported.
	if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		absolute, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		command := exec.Command("git", "-c", "safe.directory="+filepath.ToSlash(absolute), "-C", absolute, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
		data, err := command.Output()
		if err != nil {
			return nil, err
		}
		failures = append(failures, registry.CheckFiles(root)...)
		registered := []string{".github", ".githooks", "src", "tests", "contracts", "doc", "tools", "config", "prompts"}
		for _, name := range strings.Split(string(data), "\x00") {
			parts := strings.Split(name, "/")
			if len(parts) > 1 && !has(registered, parts[0]) {
				failures = append(failures, name+": unregistered top-level directory")
			}
			if strings.HasPrefix(name, "src/") && (len(parts) < 3 || parts[1] != "factorforge") {
				failures = append(failures, name+": source outside factorforge")
			}
			if strings.HasSuffix(name, ".py") && name != "contracts/check_contract.py" {
				failures = append(failures, name+": retired Python source in active tree")
			}
			if strings.HasPrefix(name, "src/") && (strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".mjs") || strings.HasSuffix(name, ".tsx")) {
				if m := registry.moduleOf(name); m == nil || !has(m.Languages, "typescript") {
					failures = append(failures, name+": unregistered JavaScript/TypeScript module")
				}
			}
		}
	}
	err = filepath.WalkDir(filepath.Join(root, "src"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && has([]string{"node_modules", ".vite", ".pnpm-store"}, entry.Name()) {
			return filepath.SkipDir
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(relative)
		fail := func(reason string) { failures = append(failures, name+": "+reason) }
		own := registry.moduleOf(name)
		if own == nil {
			fail("Go source outside registered module")
			return nil
		}
		if !has(own.Languages, "go") {
			fail("Go source in a module that does not declare Go")
			return nil
		}
		if strings.HasSuffix(name, "_test.go") {
			fail("tests belong under tests")
			return nil
		}
		rest := strings.TrimPrefix(strings.TrimPrefix(name, own.Path), "/")
		area, _, _ := strings.Cut(rest, "/")
		if strings.Count(rest, "/") == 0 {
			fail("Go source directly in the module root")
			return nil
		}
		if own.Standard && !has(standardAreas, area) {
			fail("unregistered module area")
		}
		base := module + own.Path + "/"
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if strings.HasPrefix(imported, module+"src/factorforge/") {
				target := registry.moduleOf(strings.TrimPrefix(imported, module))
				switch {
				case target == nil:
					fail("import of an unregistered path")
				case target.ID != own.ID:
					public := prefix(imported, module+target.Path+"/", target.Public)
					switch {
					case target.Layer != own.Layer && !registry.uses(own.Layer, target.Layer):
						fail("upward layer import")
					case !public:
						fail("module " + own.ID + " requires the public packages of module " + target.ID)
					}
				}
			}
			if area == "domain" && strings.HasPrefix(imported, base) && !strings.HasPrefix(imported, base+"domain") {
				fail("domain depends on outer package")
			}
			if has([]string{"application", "operations", "reports", "routing", "submission"}, area) && prefix(imported, base, []string{"api", "workers", "adapters", "entrypoints"}) {
				fail("reversed application dependency")
			}
			if (area == "ports" || area == "adapters") && prefix(imported, base, []string{"application", "api", "workers", "entrypoints"}) {
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
