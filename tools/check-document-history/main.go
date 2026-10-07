package main

import (
	"fmt"
	guard "github.com/AceNanako0721/Factorforge/tools/repoguard"
	"os"
)

func main() {
	if err := guard.CheckHistory("."); err != nil {
		fmt.Fprintln(os.Stderr, "BLOCKED:", err)
		os.Exit(1)
	}
	fmt.Println("OK: 200 historical IDs, 55 layer requirements, ADRs, links, archive hashes and legacy Word parity")
}
