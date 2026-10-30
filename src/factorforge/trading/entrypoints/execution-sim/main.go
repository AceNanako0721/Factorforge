// Command execution-sim runs a SIM run on the exchange demo account: real
// venue orders, endpoint locked to the demo URL (S1-002).
package main

import (
	r "github.com/AceNanako0721/Factorforge/src/factorforge/trading/entrypoints/assembly"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/entrypoints/venueexec"
)

func main() { r.Exit(venueexec.Main("SIM")) }
