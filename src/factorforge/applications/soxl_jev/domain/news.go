package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"time"
)

// NewsItem is one collected headline (S3-023). It never carries a price.
type NewsItem struct {
	ItemID       string    `json:"item_id"`
	Source       string    `json:"source"`
	Title        string    `json:"title"`
	Summary      string    `json:"summary"`
	Link         string    `json:"link"`
	PublishedAt  time.Time `json:"published_at"`
	ReceivedAt   time.Time `json:"received_at"`
	LicenseScope string    `json:"license_scope"`
	Recap        bool      `json:"recap"`
}

// NewsItemID is stable per source and link (title when no link).
func NewsItemID(source, link, title string) string {
	key := link
	if key == "" {
		key = title
	}
	h := sha256.Sum256([]byte(source + "|" + key))
	return hex.EncodeToString(h[:])[:24]
}

var (
	recapNumber = regexp.MustCompile(`\d+(\.\d+)?\s*(%|％|个百分点|点|bp|points?)`)
	moveEN      = regexp.MustCompile(`(?i)\b(rise|rises|rose|fall|falls|fell|jump|jumps|jumped|slide|slides|slid|drop|drops|dropped|rally|rallies|rallied|surge|surges|surged|tumble|tumbles|tumbled|sink|sinks|sank|gain|gains|gained|slip|slips|slipped|soar|soars|soared|plunge|plunges|plunged|higher|lower|climb|climbs|climbed|rebound|rebounds)\b`)
	moveZH      = regexp.MustCompile(`涨|跌|拉升|跳水|走高|走低|日高|日低|收复|回落|反弹|下挫|飙升|暴跌|大涨|大跌|翻红|翻绿`)
	newsZH      = regexp.MustCompile(`宣布|公布|发布|表示|称|据|预计|报道|计划|批准|起诉|调查|裁定|签署|指引|财报|业绩|关税|出口|制裁|会议纪要|讲话|预期|下调|上调|评级|违约|债|前值`)
	newsEN      = regexp.MustCompile(`(?i)\b(said|says|announce|announced|report|reported|reports|guidance|earnings|revenue|deal|acquire|tariff|export|sanction|ban|probe|lawsuit|filing|ruling|minutes|speech|forecast|outlook|plans?|downgrad\w*|upgrad\w*|expect\w*|estimat\w*|warn\w*|default\w*|bonds?|cuts?|raises?|bets?|options?)\b`)
)

// PriceRecap reports a headline that only restates a price move: a move word
// plus a number or percentage, without any new-information verb. Recaps are
// dropped so the model never sees price information (S3-023/S3-024).
func PriceRecap(text string) bool {
	move := moveEN.MatchString(text) || moveZH.MatchString(text)
	news := newsEN.MatchString(text) || newsZH.MatchString(text)
	return move && recapNumber.MatchString(text) && !news
}

// NewsFresh is the freshness gate (S3-024): the latest successful collection
// must be recent, and an interrupted source with no recent news blocks a run.
func NewsFresh(lastSuccess time.Time, interrupted bool, recent int, now time.Time, maxAge time.Duration) bool {
	if lastSuccess.IsZero() || now.Sub(lastSuccess) > maxAge {
		return false
	}
	return !(interrupted && recent == 0)
}

// CleanText collapses whitespace and strips markup from a feed field.
func CleanText(v string, limit int) string {
	v = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(v, "")
	v = strings.Join(strings.Fields(v), " ")
	if r := []rune(v); len(r) > limit {
		v = string(r[:limit])
	}
	return v
}
