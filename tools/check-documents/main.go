package main

import (
	"fmt"
	"os"
	"strings"

	guard "github.com/AceNanako0721/Factorforge/tools/repoguard"
)

func main() {
	data, err := os.ReadFile("VERSION")
	if err != nil {
		fmt.Fprintln(os.Stderr, "BLOCKED: VERSION missing")
		os.Exit(1)
	}
	count, err := guard.CheckDocuments(".", strings.TrimSpace(string(data)))
	if err != nil {
		fmt.Fprintln(os.Stderr, "BLOCKED:", err)
		os.Exit(1)
	}
	fmt.Printf("OK: %d Markdown documents; pairs, version headers, links, anchors and configuration field coverage\n", count)
}
