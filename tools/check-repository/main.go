package main

import (
	"flag"
	"fmt"
	guard "github.com/AceNanako0721/Factorforge/tools/repoguard"
	"os"
)

func main() {
	base := flag.String("base", "", "base Git commit")
	kind := flag.String("change-kind", "", "Change-Type")
	flag.Parse()
	if *base != "" && *kind == "" {
		value, err := guard.ParseKind(os.Getenv("PR_BODY"))
		if err != nil {
			fmt.Fprintln(os.Stderr, "BLOCKED:", err)
			os.Exit(1)
		}
		*kind = value
	}
	version, err := guard.CheckRepository(".", *base, *kind)
	if err != nil {
		fmt.Fprintln(os.Stderr, "BLOCKED:", err)
		os.Exit(1)
	}
	fmt.Printf("OK: v%s; paired documents; immutable releases and specification rules\n", version)
}
