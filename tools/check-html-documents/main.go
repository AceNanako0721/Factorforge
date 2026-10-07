package main

import (
	"encoding/json"
	"fmt"
	guard "github.com/AceNanako0721/Factorforge/tools/repoguard"
	"os"
)

func main() {
	data, err := os.ReadFile("doc/releases.json")
	var manifest guard.Manifest
	if err != nil || json.Unmarshal(data, &manifest) != nil || len(manifest.Releases) == 0 {
		fmt.Fprintln(os.Stderr, "INVALID_RELEASE_MANIFEST")
		os.Exit(1)
	}
	r := manifest.Releases[len(manifest.Releases)-1]
	count, err := guard.CheckHTML(".", r.Directory, r.Version, r.Pairs)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("OK: %d standalone HTML documents; pairs, links, SVG and API references\n", count)
}
