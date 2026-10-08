// Generate frozen public schema inputs, never source or private data.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	out := map[string]any{}
	for _, service := range []string{"trading", "strategy", "instances"} {
		raw, e := os.ReadFile("contracts/v2/" + service + "/openapi.json")
		if e != nil {
			panic(e)
		}
		var doc map[string]any
		if json.Unmarshal(raw, &doc) != nil {
			panic("schema")
		}
		schemas := doc["components"].(map[string]any)["schemas"]
		routes := map[string]any{}
		for path, v := range doc["paths"].(map[string]any) {
			op, ok := v.(map[string]any)["get"].(map[string]any)
			if !ok {
				continue
			}
			for status, response := range op["responses"].(map[string]any) {
				if status != "200" {
					continue
				}
				content, ok := response.(map[string]any)["content"].(map[string]any)
				if !ok {
					continue
				}
				body, ok := content["application/json"].(map[string]any)
				if ok {
					routes[path] = body["schema"]
				}
			}
		}
		out[service] = map[string]any{"components": map[string]any{"schemas": schemas}, "routes": routes}
	}
	data, _ := json.MarshalIndent(out, "", "  ")
	data = append(data, '\n')
	path := filepath.FromSlash("src/factorforge/applications/console/domain/projections.json")
	if len(os.Args) > 1 && os.Args[1] == "--check" {
		old, _ := os.ReadFile(path)
		if !bytes.Equal(old, data) {
			panic("console schema inputs changed")
		}
		fmt.Println("OK: console projection schema inputs match public contracts")
		return
	}
	os.MkdirAll(filepath.Dir(path), 0755)
	if e := os.WriteFile(path, data, 0644); e != nil {
		panic(e)
	}
}
