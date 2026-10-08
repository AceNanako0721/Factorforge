package main

import (
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/api"
	"os"
)

func main() {
	if e := os.MkdirAll("contracts/v2/console", 0755); e != nil {
		panic(e)
	}
	if e := os.WriteFile("contracts/v2/console/openapi.json", api.OpenAPI(), 0644); e != nil {
		panic(e)
	}
}
