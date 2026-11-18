// Synthetic native child for adapter tests. Never a vendor CLI or login method.
package main

import (
	"encoding/json"
	"fmt"
	"io"
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
	if len(args) == 12 && args[0] == "-p" {
		generate(args)
		return
	}
	if len(args) == 0 || strings.Join(args, " ") == "auth login --claudeai" || strings.Join(args, " ") == "auth logout" {
		fmt.Println("SYNTHETIC_NATIVE_HANDOFF_OK")
		return
	}
	os.Exit(23)
}

// generate answers one headless call; the model name selects the outcome.
func generate(args []string) {
	want := []string{"-p", "--model", args[2], "--output-format", "json", "--safe-mode", "--tools", "",
		"--strict-mcp-config", "--no-session-persistence", "--system-prompt"}
	if strings.Join(args[:11], "\x00") != strings.Join(want, "\x00") || args[11] == "" {
		os.Exit(24)
	}
	input, _ := io.ReadAll(os.Stdin)
	model := args[2]
	reply := map[string]any{"type": "result", "subtype": "success", "is_error": false, "stop_reason": "end_turn",
		"result":     "system=" + args[11] + " input=" + string(input),
		"usage":      map[string]any{"input_tokens": 2, "cache_creation_input_tokens": 5, "cache_read_input_tokens": 3, "output_tokens": 4},
		"modelUsage": map[string]any{model: map[string]any{"inputTokens": 2}}}
	switch model {
	case "claude-fixture-limited":
		reply["is_error"], reply["subtype"], reply["api_error_status"] = true, "error_during_execution", 429
	case "claude-fixture-fallback":
		reply["modelUsage"] = map[string]any{"claude-fixture-other": map[string]any{}}
	case "claude-fixture-cut":
		reply["stop_reason"] = "max_tokens"
	case "claude-fixture-garbled":
		fmt.Println("not json")
		os.Exit(1)
	}
	json.NewEncoder(os.Stdout).Encode(reply)
	if reply["is_error"] == true || model == "claude-fixture-exit" {
		os.Exit(1)
	}
}
