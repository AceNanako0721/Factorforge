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
	} {
		t.Run(row.path+"/"+row.imported, func(t *testing.T) {
			root := t.TempDir()
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
