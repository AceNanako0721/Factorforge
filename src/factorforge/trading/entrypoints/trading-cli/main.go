package main

import (
	"encoding/json"
	"flag"
	"fmt"
	c "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/configuration"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/httptrading"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	r "github.com/AceNanako0721/Factorforge/src/factorforge/trading/entrypoints/runtime"
	"net/url"
	"os"
	"strings"
	"time"
)

func main() { r.Exit(run()) }
func run() error {
	profile := flag.String("config", "config/config.toml", "private API profile")
	command := flag.String("command", "", "trading command")
	requestFile := flag.String("request", "", "JSON command file")
	runID := flag.String("run-id", "", "query run")
	resource := flag.String("resource-id", "", "resource")
	// Keep the existing positional command form as well as --command. Move
	// only a standalone command, never a value belonging to another flag.
	arguments := os.Args[1:]
	for i := 0; i < len(arguments); i++ {
		if strings.HasPrefix(arguments[i], "-") {
			if !strings.Contains(arguments[i], "=") {
				i++
			}
			continue
		}
		arguments = append(append(append([]string{}, arguments[:i]...), "--command="+arguments[i]), arguments[i+1:]...)
		break
	}
	if err := flag.CommandLine.Parse(arguments); err != nil {
		return err
	}
	config, err := c.Load(*profile)
	if err != nil {
		return err
	}
	if err = config.ValidateAPI(); err != nil {
		return err
	}
	ctx, cancel := r.Context()
	defer cancel()
	client := &httptrading.Client{HTTP: r.Client(15 * time.Second), Endpoint: config.Services.TradingAPIURL, Token: config.Credentials.TradingAPIToken}
	queries := map[string]string{"account-show": "account", "market-read": "market/points", "fills-show": "fills", "targets-show": "targets"}
	routes := map[string]string{"sim-create": "runs", "order-submit": "orders", "protection-set": "protections", "external-import": "external-facts", "external-resolve": "external-facts/resolve", "executor-fence": "executors/fence", "fx-register": "fx"}
	method, route := "POST", routes[*command]
	var body json.RawMessage
	var query url.Values
	if path, ok := queries[*command]; ok {
		if *runID == "" {
			return &d.Error{Code: "RUN_ID_REQUIRED", Status: 422}
		}
		method, route = "GET", path
		query = url.Values{"environment": {config.Runtime.Environment}, "account_id": {config.Trading.AccountID}, "run_id": {*runID}}
	} else {
		data, err := os.ReadFile(*requestFile)
		if err != nil || !json.Valid(data) {
			return &d.Error{Code: "REQUEST_FILE_REQUIRED", Status: 422}
		}
		body = data
		switch *command {
		case "order-cancel":
			if *resource == "" {
				return &d.Error{Code: "RESOURCE_ID_REQUIRED", Status: 422}
			}
			route = "orders/" + url.PathEscape(*resource) + "/cancel"
		case "reconcile", "stop", "resume":
			var metadata d.Command
			if json.Unmarshal(body, &metadata) != nil {
				return &d.Error{Code: "INVALID_REQUEST", Status: 422}
			}
			route = "runs/" + url.PathEscape(metadata.RunKey.RunID) + "/" + *command
		}
	}
	if route == "" {
		return &d.Error{Code: "COMMAND_UNSUPPORTED", Status: 422}
	}
	result, err := client.Call(ctx, method, "/api/v2/trading/"+route, query, body)
	if err != nil {
		return err
	}
	fmt.Println(string(result))
	return nil
}
