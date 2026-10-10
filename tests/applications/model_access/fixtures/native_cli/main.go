// Synthetic native child for adapter tests. Never a vendor CLI or login method.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "OPENAI_API_KEY", "FACTORFORGE_TRADING_TOKEN", "GOOGLE_API_KEY"} {
		if os.Getenv(key) != "" {
			os.Exit(21)
		}
	}
	cwd, _ := os.Getwd()
	if !strings.Contains(filepath.ToSlash(cwd), "/runtime/model-cli-workspace/") {
		os.Exit(22)
	}
	args := os.Args[1:]
	if len(args) == 1 && args[0] == "--version" {
		if strings.Contains(filepath.Base(os.Args[0]), "agy") {
			fmt.Println("1.3.3")
		} else {
			fmt.Println("2.1.296 (Claude Code)")
		}
		return
	}
	if strings.Join(args, " ") == "auth status --json" {
		json.NewEncoder(os.Stdout).Encode(map[string]any{"loggedIn": true, "authMethod": "claude.ai", "email": "fixture@example.invalid", "organizationId": "fixture-private-id"})
		return
	}
	if len(args) == 0 || strings.Join(args, " ") == "auth login --claudeai" || strings.Join(args, " ") == "auth logout" {
		fmt.Println("SYNTHETIC_NATIVE_HANDOFF_OK")
		return
	}
	os.Exit(23)
}
