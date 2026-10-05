// Export the native P1 frozen public contract without configuration or a server.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/api"
	"os"
)

func main() {
	check := flag.Bool("check", false, "compare the current frozen contract")
	flag.Parse()
	var pretty bytes.Buffer
	if json.Indent(&pretty, []byte(api.OpenAPI), "", "  ") != nil {
		fmt.Fprintln(os.Stderr, "INVALID_COMPILED_CONTRACT")
		os.Exit(1)
	}
	pretty.WriteByte('\n')
	path := "contracts/v2/trading/openapi.json"
	if *check {
		current, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(current, pretty.Bytes()) {
			fmt.Fprintln(os.Stderr, "TRADING_CONTRACT_DIFFERS")
			os.Exit(1)
		}
		fmt.Println("OK: native P1 contract identical")
		return
	}
	if err := os.WriteFile(path, pretty.Bytes(), 0644); err != nil {
		fmt.Fprintln(os.Stderr, "CONTRACT_WRITE_FAILED")
		os.Exit(1)
	}
	fmt.Println("Exported trading-2.0 contract")
}
