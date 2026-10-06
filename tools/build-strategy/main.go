// Build independent P2 executables; only P1's public DTOs are imported by source.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func run() error {
	out := flag.String("out", "runtime/go-bin", "ignored binary destination")
	flag.Parse()
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	destination, err := filepath.Abs(*out)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(filepath.Join(root, "runtime"), destination)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("BUILD_DESTINATION_MUST_BE_RUNTIME")
	}
	if err = os.MkdirAll(destination, 0700); err != nil {
		return err
	}
	for _, name := range []string{"strategy-api", "strategy-scheduler", "strategy-feedback", "factorforge-strategy"} {
		source := "./src/factorforge/strategy/entrypoints/" + name
		inspect := exec.Command("go", "list", "-deps", source)
		data, e := inspect.Output()
		if e != nil {
			return fmt.Errorf("P2_DEPENDENCY_CHECK_FAILED")
		}
		if strings.Contains(string(data), "/factorforge/applications") {
			return fmt.Errorf("P2_UPPER_LAYER_DEPENDENCY")
		}
		command := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(destination, name), source)
		command.Env = append(os.Environ(), "CGO_ENABLED=0")
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if command.Run() != nil {
			return fmt.Errorf("P2_BUILD_FAILED")
		}
		fmt.Println("Built " + name)
	}
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
