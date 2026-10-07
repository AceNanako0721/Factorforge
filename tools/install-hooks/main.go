// Install only this checkout's public-content hooks. No global settings change.
package main

import (
	"fmt"
	"os"
	"runtime"

	guard "github.com/AceNanako0721/Factorforge/tools/repoguard"
)

func main() {
	for _, name := range []string{"pre-commit", "pre-push"} {
		path := ".githooks/" + name
		info, err := os.Stat(path)
		if err != nil {
			fail()
		}
		if runtime.GOOS != "windows" && os.Chmod(path, info.Mode()|0111) != nil {
			fail()
		}
	}
	if _, err := guard.Git(".", "config", "--local", "core.hooksPath", ".githooks"); err != nil {
		fail()
	}
	fmt.Println("Installed this checkout's Go public-content hooks")
}
func fail() { fmt.Fprintln(os.Stderr, "HOOK_INSTALLATION_FAILED"); os.Exit(1) }
