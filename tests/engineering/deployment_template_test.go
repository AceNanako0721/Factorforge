package engineering_test

import (
	guard "github.com/AceNanako0721/Factorforge/tools/repoguard"
	"os"
	"strings"
	"testing"
)

func TestDeploymentTemplateRejectsPrivateValuesAndKeepsFrozenHistory(t *testing.T) {
	raw, err := os.ReadFile("../../config/config.example.toml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if guard.TemplateIssue("config/config.example.toml", raw, false) != "" {
		t.Fatal("empty deployment template rejected")
	}
	start, end := strings.Index(text, "[deployment]"), strings.Index(text, "# BEGIN FACTORFORGE MODEL ACCESS")
	legacy := text[:start] + text[end:]
	if guard.TemplateIssue("config/config.example.toml", []byte(legacy), true) != "" || guard.TemplateIssue("config/config.example.toml", []byte(legacy), false) == "" {
		t.Fatal("frozen history policy violated")
	}
	for _, pair := range [][2]string{{"data_root = \"\"", "data_root = \"/private/data\""}, {"password = \"\"", "password = \"short\""}, {"ff_p1 = \"\"", "ff_p1 = \"short\""}, {"risk_policy_reviewed = false", "risk_policy_reviewed = true"}, {"[deployment.profiles]", "[deployment.profiles]\nsecret = \"x\""}} {
		changed := strings.Replace(text, pair[0], pair[1], 1)
		if changed == text {
			t.Fatal("fixture field missing")
		}
		for _, history := range []bool{false, true} {
			if guard.TemplateIssue("config/config.example.toml", []byte(changed), history) == "" {
				t.Fatal("private deployment value admitted")
			}
		}
	}
}
