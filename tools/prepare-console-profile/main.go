package main

import (
	"flag"
	"fmt"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/config"
	"os"
)

func main() {
	canonical := flag.String("config", "config/config.toml", "canonical private configuration")
	out := flag.String("out", "runtime/console/profile.json", "exclusive private derived profile")
	flag.Parse()
	if e := config.Prepare(*canonical, *out); e != nil {
		fmt.Fprintln(os.Stderr, "CONSOLE_PROFILE_PREPARATION_FAILED")
		os.Exit(1)
	}
	fmt.Println("OK: private console-only profile created")
}
