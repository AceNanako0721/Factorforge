package main

import (
	"flag"
	config "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters/configuration"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/entrypoints/assembly"
)

func main() {
	path := flag.String("config", "config/config.toml", "private canonical configuration")
	internal := flag.Bool("internal", false, "scoped workload listener")
	flag.Parse()
	c, e := config.Load(*path)
	if e != nil {
		assembly.Exit(e)
	}
	ctx, cancel := assembly.Context()
	defer cancel()
	assembly.Exit(assembly.Serve(ctx, c, *internal))
}
