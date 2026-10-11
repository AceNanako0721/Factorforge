package engineering_test

import (
	"encoding/json"
	guard "github.com/AceNanako0721/Factorforge/tools/contractguard"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSemanticPublicGenerationReplySchema(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "contracts/v2/model-access/omp-generate-response.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err = json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	c := guard.NewCompiler()
	if err = c.AddResource("urn:semantic:reply", document); err != nil {
		t.Fatal(err)
	}
	schema, err := c.Compile("urn:semantic:reply")
	if err != nil {
		t.Fatal(err)
	}
	good := `{"v":2,"id":"fixture","ok":true,"result":{"provider":"fixture","account_id":1,"model":"fixture","text":"candidate","prompt_hash":"` + strings.Repeat("a", 64) + `","usage":{"input":1}}}`
	for _, text := range []string{good, `{"v":2,"id":"fixture","ok":false,"error":{"code":"DELIVERY_UNKNOWN","delivery_unknown":true}}`} {
		var value any
		_ = json.Unmarshal([]byte(text), &value)
		if err = schema.Validate(value); err != nil {
			t.Fatal(err)
		}
	}
	for _, text := range []string{strings.Replace(good, `"usage":{"input":1}`, `"usage":{"input":null}`, 1), strings.Replace(good, `"prompt_hash":"`+strings.Repeat("a", 64)+`",`, "", 1), strings.Replace(good, `"result":`, `"error":null,"result":`, 1), strings.Replace(good, `"account_id":1`, `"account_id":0`, 1)} {
		var value any
		_ = json.Unmarshal([]byte(text), &value)
		if schema.Validate(value) == nil {
			t.Fatal("invalid public reply admitted")
		}
	}
}
