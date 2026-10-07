// Local presence diagnostics only; prints no private field values.
package main

import (
	"encoding/json"
	"flag"
	"os"

	c "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/configuration"
)

func main() {
	path := flag.String("config", "config/config.toml", "private configuration")
	flag.Parse()
	if json.NewEncoder(os.Stdout).Encode(c.Inspect(*path)) != nil {
		os.Exit(1)
	}
}
