// Command execution-live runs a LIVE run (real trading) behind the LIVE
// admission gates.
package main

import (
	r "github.com/AceNanako0721/Factorforge/src/factorforge/trading/entrypoints/assembly"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/entrypoints/venueexec"
)

func main() { r.Exit(venueexec.Main("LIVE")) }
