package configuration

import (
	"net/url"
	"os"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Inspect reports presence only. It does not validate production admission,
// connect to a venue/database, or return private values in diagnostics.
func Inspect(path string) map[string]bool {
	data, err := os.ReadFile(path)
	var value map[string]any
	if err != nil || toml.Unmarshal(data, &value) != nil {
		return map[string]bool{"configuration_parseable": false}
	}
	sections := map[string]map[string]any{}
	for _, name := range []string{"services", "credentials", "trading"} {
		if raw, exists := value[name]; exists {
			section, ok := raw.(map[string]any)
			if !ok {
				return map[string]bool{"configuration_parseable": false}
			}
			sections[name] = section
		} else {
			sections[name] = map[string]any{}
		}
	}
	present := func(section, name string) bool {
		switch v := sections[section][name].(type) {
		case string:
			return v != ""
		case map[string]any:
			return len(v) > 0
		default:
			return false
		}
	}
	endpoint, _ := sections["services"]["exchange_api_url"].(string)
	return map[string]bool{"configuration_parseable": true, "official_futures_testnet_endpoint": OfficialTestnet(endpoint), "api_key_present": present("credentials", "exchange_api_key"), "api_secret_present": present("credentials", "exchange_api_secret"), "execution_database_present": present("services", "execution_database_url"), "account_binding_present": present("trading", "account_id"), "signed_transport_policy_present": present("trading", "signed_transport"), "readiness_record_present": present("trading", "live_readiness")}
}

func OfficialTestnet(endpoint string) bool {
	u, err := url.Parse(endpoint)
	return err == nil && u.Scheme == "https" && strings.EqualFold(u.Hostname(), "demo-fapi.binance.com") && (u.Port() == "" || u.Port() == "443") && u.User == nil && (u.Path == "" || u.Path == "/") && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.Opaque == ""
}
