package analysis

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
)

// JudgmentTemplate is the private news-judgment instruction text (S3-024).
// P3 owns it and sends the rendered text as the single user message; the
// public repository carries only the empty example asset.
type JudgmentTemplate struct {
	Version string
	Text    string
}

var placeholders = []string{"{{NOW}}", "{{PREVIOUS}}", "{{RECENT_COUNT}}", "{{RECENT}}", "{{OLDER_COUNT}}", "{{OLDER}}"}

func LoadJudgmentTemplate(path, version string, maxBytes int) (JudgmentTemplate, error) {
	var t JudgmentTemplate
	if maxBytes <= 0 || !d.ValidID(version) || !filepath.IsAbs(path) || strings.HasSuffix(filepath.Base(path), ".example.json") {
		return t, d.Fail("PRIVATE_PROMPT_REQUIRED", 503)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > int64(maxBytes) {
		return t, d.Fail("PRIVATE_PROMPT_UNAVAILABLE", 503)
	}
	f, err := os.Open(path)
	if err != nil {
		return t, d.Fail("PRIVATE_PROMPT_UNAVAILABLE", 503)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, int64(maxBytes)+1))
	if err != nil || len(raw) > maxBytes {
		return t, d.Fail("PRIVATE_PROMPT_UNAVAILABLE", 503)
	}
	var file struct {
		SchemaVersion int    `json:"schema_version"`
		AssetKind     string `json:"asset_kind"`
		PromptVersion string `json:"prompt_version"`
		Template      string `json:"judgment_template"`
	}
	if d.DecodePrivate(raw, &file) != nil || file.SchemaVersion != 1 || file.AssetKind != "PRIVATE" || file.PromptVersion != version {
		return t, d.Fail("PRIVATE_PROMPT_INVALID", 503)
	}
	for _, p := range placeholders {
		if strings.Count(file.Template, p) != 1 {
			return t, d.Fail("PRIVATE_PROMPT_INVALID", 503)
		}
	}
	return JudgmentTemplate{Version: version, Text: file.Template}, nil
}

var spaces = regexp.MustCompile(`\s+`)

// RenderJudgment builds the model input: recent items with summaries and older
// titles only. No price, return or position information is ever included.
func RenderJudgment(t JudgmentTemplate, now time.Time, previous *d.JudgmentOutput, recent, older []d.NewsItem) (string, []string, string) {
	tokyo, _ := time.LoadLocation("Asia/Tokyo")
	ny, _ := time.LoadLocation("America/New_York")
	line := func(x d.NewsItem, full bool) string {
		body := x.Title
		if full && x.Summary != "" {
			body += " — " + x.Summary
		}
		if body == "" {
			body = x.Summary
		}
		body = spaces.ReplaceAllString(body, " ")
		if r := []rune(body); len(r) > 400 {
			body = string(r[:400])
		}
		return "[" + d.ShortID(x.ItemID) + "] " + x.ReceivedAt.In(tokyo).Format("01-02 15:04 JST") + " " + x.Source + ": " + body
	}
	ids := []string{}
	rl, ol := []string{}, []string{}
	for _, x := range recent {
		ids = append(ids, x.ItemID)
		rl = append(rl, line(x, true))
	}
	for _, x := range older {
		ids = append(ids, x.ItemID)
		ol = append(ol, line(x, false))
	}
	prev := "null"
	if previous != nil {
		raw, _ := json.Marshal(map[string]any{"direction": previous.Direction, "confidence": previous.Confidence, "thesis": previous.Thesis, "invalidation": previous.Invalidation})
		prev = string(raw)
	}
	text := strings.NewReplacer(
		"{{NOW}}", now.In(tokyo).Format("2006-01-02 15:04 JST")+" / "+now.In(ny).Format("15:04 ET"),
		"{{PREVIOUS}}", prev,
		"{{RECENT_COUNT}}", strconv.Itoa(len(recent)),
		"{{RECENT}}", strings.Join(rl, "\n"),
		"{{OLDER_COUNT}}", strconv.Itoa(len(older)),
		"{{OLDER}}", strings.Join(ol, "\n"),
	).Replace(t.Text)
	h := sha256.Sum256([]byte(text))
	return text, ids, hex.EncodeToString(h[:])
}
