package main

import (
	"flag"
	"fmt"
	guard "github.com/AceNanako0721/Factorforge/tools/repoguard"
	"os"
)

func main() {
	index := flag.Bool("index", false, "check actual staged blobs")
	revision := flag.String("revision", "", "commit to check")
	history := flag.Bool("all-history", false, "check all reachable history")
	flag.Parse()
	if *index && (*history || *revision != "") || *history && *revision != "" {
		fmt.Fprintln(os.Stderr, "INVALID_SCAN_OPTIONS")
		os.Exit(1)
	}
	issues, count, err := guard.Scan(".", *revision, *history)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, issue := range issues {
		fmt.Fprintf(os.Stderr, "BLOCKED %s: %s\n", issue.Path, issue.Rule)
	}
	if len(issues) > 0 {
		os.Exit(1)
	}
	fmt.Printf("OK: %d Git file versions; empty public templates; no detected credentials\n", count)
}
