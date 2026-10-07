package main

import (
	"fmt"
	"os"
	"os/exec"
)

func main() {
	if os.MkdirAll("runtime/bin", 0700) != nil {
		panic("INSTANCE_BUILD_FAILED")
	}
	cmd := exec.Command("go", "build", "-o", "runtime/bin/soxl-jev-api", "./src/factorforge/applications/soxl_jev/entrypoints/soxl-jev-api")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if cmd.Run() != nil {
		os.Exit(1)
	}
	fmt.Println("built instance read API; no ingest/analysis worker or LIVE admission implied")
}
