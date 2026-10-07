package main

import (
	"fmt"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api"
	"os"
	"path/filepath"
)

func main() {
	for name, internal := range map[string]bool{"openapi.json": false, "workload.openapi.json": true} {
		if err := os.WriteFile(filepath.Join("contracts", "v2", "strategy", name), api.OpenAPI(internal), 0644); err != nil {
			fmt.Fprintln(os.Stderr, "STRATEGY_CONTRACT_EXPORT_FAILED")
			os.Exit(1)
		}
	}
	fmt.Println("Exported strategy-2.0 public and workload contracts with S2-024")
}
