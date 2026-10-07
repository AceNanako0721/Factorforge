package main

import (
	"fmt"
	"github.com/AceNanako0721/Factorforge/tools/contractguard"
	"os"
)

func main() {
	operations, fixtures, err := contractguard.Check(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, "BLOCKED:", err)
		os.Exit(1)
	}
	fmt.Printf("OK: OpenAPI 3.1, %d schema fixtures, frozen surface, %d native stubs and v2 schemas\n", fixtures, operations)
}
