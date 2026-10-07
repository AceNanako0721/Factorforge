package main

import (
	"flag"
	"fmt"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/config"
	"os"
	"path/filepath"
)

func main() {
	source := flag.String("config", "config/config.toml", "Canonical private config")
	output := flag.String("output", "runtime/instance-profiles", "New private output directory")
	flag.Parse()
	path, err := filepath.Abs(*source)
	if err == nil {
		_, err = config.PrepareWorkerProfiles(path, *output)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "INSTANCE_PROFILE_PREPARATION_FAILED")
		os.Exit(1)
	}
	fmt.Println("Prepared isolated INGEST/RESEARCH/TRADING profiles; no services started")
}
