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
	for _, entry := range []string{"soxl-api", "judgment-worker", "position-manager", "calendar-worker", "instance-cli"} {
		cmd := exec.Command("go", "build", "-o", "runtime/bin/"+entry, "./src/factorforge/applications/soxl/entrypoints/"+entry)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if cmd.Run() != nil {
			os.Exit(1)
		}
	}
	fmt.Println("built the read API, judgment worker, position manager, calendar worker and operator CLI")
}
