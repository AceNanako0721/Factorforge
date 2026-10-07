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
	for _, entry := range []string{"soxl-jev-api", "ingest-worker", "research-analysis-worker", "trading-analysis-worker", "instance-cli"} {
		cmd := exec.Command("go", "build", "-o", "runtime/bin/"+entry, "./src/factorforge/applications/soxl_jev/entrypoints/"+entry)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if cmd.Run() != nil {
			os.Exit(1)
		}
	}
	fmt.Println("built instance API, three workers and operator CLI; provider and LIVE admission remain separate")
}
