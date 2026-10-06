package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	config "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters/configuration"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/entrypoints/assembly"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func run() error {
	args := flag.NewFlagSet("factorforge-strategy", flag.ContinueOnError)
	path := args.String("config", "config/config.toml", "private canonical configuration")
	internal := args.Bool("internal", false, "manual SIM workload client")
	file := args.String("file", "", "generic JSON payload")
	objectID := args.String("object-id", "", "observed object ID")
	eventID := args.String("event-id", "", "event ID")
	// Keep legacy argparse's flags before or after the subcommand.
	flags := []string{}
	positionals := []string{}
	for i := 0; i < len(os.Args[1:]); i++ {
		value := os.Args[i+1]
		if strings.HasPrefix(value, "-") {
			flags = append(flags, value)
			if value != "--internal" && !strings.Contains(value, "=") {
				i++
				if i >= len(os.Args[1:]) {
					return &d.Error{Code: "CLI_ARGUMENT_INVALID", Status: 422}
				}
				flags = append(flags, os.Args[i+1])
			}
		} else {
			positionals = append(positionals, value)
		}
	}
	if args.Parse(append(flags, positionals...)) != nil || args.NArg() != 1 {
		return &d.Error{Code: "CLI_ARGUMENT_INVALID", Status: 422}
	}
	c, err := config.Load(*path)
	if err != nil {
		return err
	}
	ctx, cancel := assembly.Context()
	defer cancel()
	command := args.Arg(0)
	if command == "initialize" {
		if err = assembly.Initialize(ctx, c); err != nil {
			return err
		}
		fmt.Println("Initialized strategy registry and separately authorized workload grants")
		return nil
	}
	if *internal && c.Environment != "SIM" {
		return &d.Error{Code: "MANUAL_WORKLOAD_CLI_SIM_ONLY", Status: 403}
	}
	address, token := c.PublicAPIURL, c.PublicToken
	if *internal {
		address, token = c.InternalAPIURL, c.WorkloadToken
	}
	clientBinding, err := adapters.NewTradingV2(address, token, time.Duration(c.TimeoutSeconds*float64(time.Second)), c.CandleInterval, c.HistorySeconds)
	if err != nil {
		return err
	}
	method, route := "POST", ""
	var body io.Reader
	switch command {
	case "show-pool", "show-target", "show-case":
		if *objectID == "" {
			return &d.Error{Code: "CLI_OBJECT_ID_REQUIRED", Status: 422}
		}
		method = "GET"
		resource := map[string]string{"show-pool": "pool", "show-target": "targets", "show-case": "cases"}[command]
		route = "/objects/" + url.PathEscape(*objectID) + "/" + resource
	case "create-object", "create-event", "submit-score", "advance-clock":
		route = map[string]string{"create-object": "/objects", "create-event": "/events", "submit-score": "/events/" + url.PathEscape(*eventID) + "/scores", "advance-clock": "/clock"}[command]
		if *file == "" {
			return &d.Error{Code: "CLI_PAYLOAD_REQUIRED", Status: 422}
		}
		raw, e := os.ReadFile(*file)
		if e != nil || !json.Valid(raw) {
			return &d.Error{Code: "CLI_PAYLOAD_INVALID", Status: 422}
		}
		body = bytes.NewReader(raw)
	default:
		return &d.Error{Code: "CLI_COMMAND_INVALID", Status: 422}
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(address, "/")+api.Prefix+route, body)
	if err != nil {
		return &d.Error{Code: "CLI_REQUEST_FAILED", Status: 503}
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := clientBinding.Client.Do(request)
	if err != nil {
		return &d.Error{Code: "CLI_REQUEST_FAILED", Status: 503}
	}
	defer response.Body.Close()
	var result any
	decoder := json.NewDecoder(io.LimitReader(response.Body, 4<<20))
	decoder.UseNumber()
	if decoder.Decode(&result) != nil {
		return &d.Error{Code: "CLI_RESPONSE_INVALID", Status: 503}
	}
	if response.StatusCode >= 300 {
		return &d.Error{Code: adapters.SafeCode(d.Text(d.Object(result)["code"])), Status: response.StatusCode}
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	return nil
}
func main() { assembly.Exit(run()) }
