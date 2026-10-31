package engineering_test

import (
	"github.com/AceNanako0721/Factorforge/tools/layoutguard"
	"os"
	"path/filepath"
	"testing"
)

func TestGoImportBoundary(t *testing.T) {
	for _, row := range []struct {
		path, imported string
		allowed        bool
	}{
		{"trading/domain", "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain", false},
		{"strategy/domain", "github.com/AceNanako0721/Factorforge/src/factorforge/trading/application", false},
		{"strategy/domain", "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto_private", false},
		{"strategy/domain", "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto", true},
		{"trading/domain", "net/http", false},
		{"trading/application", "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/memory", false},
		{"trading/domain", "github.com/AceNanako0721/Factorforge/tools/layoutguard", false},
		{"applications/soxl/operations", "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters/postgres", false},
		{"applications/soxl/operations", "github.com/AceNanako0721/Factorforge/src/factorforge/operations/console/api/dto", false},
		{"operations/console/api", "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto", true},
		{"operations/console/api", "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain", false},
		{"operations/console/api", "github.com/AceNanako0721/Factorforge/src/factorforge/operations/model_access/api/dto", false},
		{"strategy/domain", "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/api/dto", false},
		{"applications/soxl/operations", "github.com/AceNanako0721/Factorforge/src/factorforge/trading/application", false},
		{"applications/soxl/operations", "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto", true},
		{"applications/soxl/operations", "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto", true},
		{"applications/soxl/domain", "net/http", false},
		{"applications/soxl/domain", "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/adapters/postgres", false},
		{"operations/console/api", "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/adapters/postgres", false},
		{"applications/common/domain", "fmt", false},
	} {
		t.Run(row.path+"/"+row.imported, func(t *testing.T) {
			root := t.TempDir()
			copyRegistry(t, root)
			directory := filepath.Join(root, "src/factorforge", row.path)
			if err := os.MkdirAll(directory, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "fixture.go"), []byte("package fixture\nimport _ \""+row.imported+"\"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			failures, err := layoutguard.Check(root)
			if err != nil {
				t.Fatal(err)
			}
			if (len(failures) == 0) != row.allowed {
				t.Fatal(failures)
			}
		})
	}
}

func copyRegistry(t *testing.T, root string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", layoutguard.RegistryPath))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(root, "doc"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, layoutguard.RegistryPath), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

// The registry in the repository must describe the real tree.
func TestLayerRegistryMatchesRepository(t *testing.T) {
	failures, err := layoutguard.Check(repoRoot(t))
	if err != nil || len(failures) > 0 {
		t.Fatal(failures, err)
	}
}

func TestLayerRegistryRejectsBrokenRegistries(t *testing.T) {
	good, err := layoutguard.LoadRegistry(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if f := good.Validate(); len(f) > 0 {
		t.Fatal(f)
	}
	for name, change := range map[string]func(*layoutguard.Registry){
		"cycle":               func(r *layoutguard.Registry) { r.Layers[0].Uses = []string{"L4"} },
		"unknown layer":       func(r *layoutguard.Registry) { r.Modules[0].Layer = "L9" },
		"uncovered module":    func(r *layoutguard.Registry) { r.Specs = r.Specs[1:] },
		"nested module":       func(r *layoutguard.Registry) { r.Modules[2].Path = r.Modules[3].Path + "/inner" },
		"spec in wrong layer": func(r *layoutguard.Registry) { r.Specs[0].Layer = "L2" },
	} {
		t.Run(name, func(t *testing.T) {
			r := good
			r.Layers = append([]layoutguard.Layer(nil), good.Layers...)
			r.Modules = append([]layoutguard.Module(nil), good.Modules...)
			r.Specs = append([]layoutguard.Spec(nil), good.Specs...)
			for i := range r.Layers {
				r.Layers[i].Uses = append([]string(nil), good.Layers[i].Uses...)
			}
			change(&r)
			if len(r.Validate()) == 0 {
				t.Fatal("broken registry accepted")
			}
		})
	}
}

// A layer may hold several specs and a spec may cover several modules.
func TestLayerAllowsSeveralSpecsAndModules(t *testing.T) {
	good, err := layoutguard.LoadRegistry(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	r := good
	r.Modules = append(append([]layoutguard.Module(nil), good.Modules...), layoutguard.Module{ID: "P6", Name: "归档", Layer: "L4", Path: "src/factorforge/operations/archive", Tests: "tests/operations/archive", Languages: []string{"go"}, Standard: true})
	r.Specs = append(append([]layoutguard.Spec(nil), good.Specs...), layoutguard.Spec{ID: "S6", Name: "归档", Layer: "L4", Modules: []string{"P6"}, Spec: "doc/06_归档式样书.md", Design: "doc/06_归档设计书.md"})
	if f := r.Validate(); len(f) > 0 {
		t.Fatal(f)
	}
}
