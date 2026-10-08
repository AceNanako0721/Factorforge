// Build the independent Go BFF and check supplied browser artifacts. Building
// frontend dependencies is a separate locked step, never a production service.
package main

import (
	"flag"
	"fmt"
	"github.com/AceNanako0721/Factorforge/tools/repoguard"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	web := flag.String("web-build", "", "optional built static directory")
	flag.Parse()
	if *web != "" {
		base, e := filepath.EvalSymlinks(*web)
		if e != nil {
			panic("WEB_BUILD_REQUIRED")
		}
		if e = filepath.WalkDir(base, func(path string, entry os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlink")
			}
			if entry.IsDir() {
				return nil
			}
			name, e := filepath.Rel(base, path)
			if e != nil {
				return e
			}
			if !entry.Type().IsRegular() || strings.HasSuffix(name, ".map") || !strings.HasSuffix(name, ".js") && !strings.HasSuffix(name, ".css") && name != "index.html" && name != "THIRD_PARTY_NOTICES.txt" && name != "lightweight-charts.LICENSE.txt" && name != "lightweight-charts.NOTICE.txt" {
				return fmt.Errorf("unexpected web artifact")
			}
			raw, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			if len(repoguard.ContentIssues("web-build/"+filepath.ToSlash(name), raw, false)) > 0 {
				return fmt.Errorf("unsafe browser content")
			}
			return nil
		}); e != nil {
			panic("WEB_BUILD_INVALID")
		}
		if _, e = os.Stat(filepath.Join(base, "index.html")); e != nil {
			panic("WEB_BUILD_REQUIRED")
		}
	}
	os.MkdirAll("runtime/go-bin", 0755)
	cmd := exec.Command("go", "build", "-trimpath", "-o", "runtime/go-bin/console-api", "./src/factorforge/applications/console/entrypoints/console-api")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if e := cmd.Run(); e != nil {
		os.Exit(1)
	}
	fmt.Println("OK: independent native console API built")
}
