package main

import (
	"fmt"
	"github.com/AceNanako0721/Factorforge/tools/contractguard"
	"os"
)

func main() {
	operations, documents, err := contractguard.Check(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, "BLOCKED:", err)
		os.Exit(1)
	}
	fmt.Printf("OK: %d OpenAPI 3.1 contracts, %d operations with typed stubs and compiled schemas\n", documents, operations)
}
