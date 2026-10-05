// Derive P1 runtime profiles from the single ignored configuration source.
// This tool never connects to a database, venue or upper-layer provider.
package main

import (
	"flag"
	"fmt"
	c "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/configuration"
	"github.com/pelletier/go-toml/v2"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "PRIVATE_PROFILE_PREPARATION_FAILED")
		os.Exit(1)
	}
}
func run() error {
	path := flag.String("config", "config/config.toml", "single private source")
	destination := flag.String("out", "runtime/trading-profiles", "ignored derived profiles")
	flag.Parse()
	source, err := c.Load(*path)
	if err != nil {
		return err
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	folder, err := filepath.Abs(*destination)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(filepath.Join(root, "runtime"), folder)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("PRIVATE_PROFILE_SCOPE_INVALID")
	}
	if err = os.MkdirAll(folder, 0700); err != nil {
		return err
	}
	// Typed P1 configuration deliberately excludes strategy, model and prompts.
	api := source
	api.Credentials.ExchangeAPIKey = ""
	api.Credentials.ExchangeAPISecret = ""
	api.Services.ExecutionDatabaseURL = ""
	executor := source
	executor.Credentials.TradingAPIToken = ""
	executor.Services.DatabaseURL = ""
	executor.Services.TradingAPIURL = ""
	for name, profile := range map[string]c.Config{"api.toml": api, "execution.toml": executor} {
		data, err := toml.Marshal(profile)
		if err != nil {
			return err
		}
		path := filepath.Join(folder, name)
		file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		if err = file.Chmod(0600); err != nil {
			file.Close()
			return err
		}
		_, err = file.Write(data)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	fmt.Println("Prepared private API and execution profiles; no services started")
	return nil
}
