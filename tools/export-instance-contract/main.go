package main

import (
	"fmt"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/api"
	"os"
)

func main() {
	path := "contracts/v2/instances"
	if err := os.MkdirAll(path, 0755); err != nil {
		panic("INSTANCE_CONTRACT_EXPORT_FAILED")
	}
	if err := os.WriteFile(path+"/openapi.json", api.OpenAPI(), 0644); err != nil {
		panic("INSTANCE_CONTRACT_EXPORT_FAILED")
	}
	fmt.Println("instance read contract exported")
}
