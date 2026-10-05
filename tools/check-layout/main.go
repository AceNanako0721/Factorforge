package main

import (
	"flag"
	"fmt"
	"github.com/AceNanako0721/Factorforge/tools/layoutguard"
	"os"
)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	failures, err := layoutguard.Check(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "layout check failed:", err)
		os.Exit(1)
	}
	for _, failure := range failures {
		fmt.Println("BLOCKED", failure)
	}
	if len(failures) > 0 {
		os.Exit(1)
	}
	fmt.Println("OK: Go source placement and layer imports")
}
