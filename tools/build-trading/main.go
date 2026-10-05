// Build every standalone P1 executable from the single public Go module.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	output := flag.String("out", "runtime/go-bin", "ignored binary destination")
	flag.Parse()
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	destination, err := filepath.Abs(*output)
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
	for _, name := range []string{"trading-api", "execution-sim", "execution-live", "market-collector", "trading-cli", "testnet-acceptance"} {
		source := "./src/factorforge/trading/entrypoints/" + name
		inspect := exec.Command("go", "list", "-deps", source)
		data, err := inspect.Output()
		if err != nil {
			return fmt.Errorf("P1_DEPENDENCY_CHECK_FAILED")
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "/factorforge/strategy") || strings.Contains(line, "/factorforge/applications") {
				return fmt.Errorf("P1_UPPER_LAYER_DEPENDENCY")
			}
		}
		command := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(destination, name), source)
		command.Env = append(os.Environ(), "CGO_ENABLED=0")
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err = command.Run(); err != nil {
			return fmt.Errorf("P1_BUILD_FAILED")
		}
		fmt.Println("Built " + name)
	}
	return nil
}
