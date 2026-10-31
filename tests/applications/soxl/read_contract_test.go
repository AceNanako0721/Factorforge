package soxl_test

import (
	"bytes"
	"encoding/json"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/api"
	contract "github.com/AceNanako0721/Factorforge/tools/contractguard"
	"os"
	"testing"
)

func TestInstanceContractGeneratedAndOfflineValidated(t *testing.T) {
	raw, err := os.ReadFile("../../../contracts/v2/instances/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, api.OpenAPI()) {
		t.Fatal("instance contract drift")
	}
	var doc, meta map[string]any
	json.Unmarshal(raw, &doc)
	raw, err = os.ReadFile("../../../contracts/meta/openapi-3.1-2025-09-15.json")
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(raw, &meta)
	if err = contract.Validate(meta, doc); err != nil {
		t.Fatal(err)
	}
	if err = contract.Lint(doc, false, nil); err != nil {
		t.Fatal(err)
	}
	if err = contract.StubSmoke(doc); err != nil {
		t.Fatal(err)
	}
}
