package monitoring

import (
	"strings"
	"time"
)

// RSS permits a one-digit day. Unknown named zones must not be fabricated as
// offset zero by time.Parse; only explicit numeric offsets and UTC/GMT work.
// This parses supplier metadata, never independently proves first publication.
func ParseRSSPublished(value string) (time.Time, bool) {
	for _, layout := range []string{
		time.RFC1123Z, "Mon, 2 Jan 2006 15:04:05 -0700",
		"Mon, 02 Jan 2006 15:04:05 GMT", "Mon, 2 Jan 2006 15:04:05 GMT",
		"Mon, 02 Jan 2006 15:04:05 UTC", "Mon, 2 Jan 2006 15:04:05 UTC",
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), true
		}
	}
	// BEA publishes US Eastern abbreviations instead of a numeric offset.
	// Only EST/EDT are mapped, and only through the named zone so the date's
	// daylight-saving state decides the offset.
	if strings.HasSuffix(value, " EST") || strings.HasSuffix(value, " EDT") {
		ny, err := time.LoadLocation("America/New_York")
		if err == nil {
			for _, layout := range []string{"Mon, 2 Jan 2006 15:04:05 MST", "Mon, 02 Jan 2006 15:04:05 MST"} {
				if parsed, err := time.ParseInLocation(layout, value, ny); err == nil {
					return parsed.UTC(), true
				}
			}
		}
	}
	return time.Time{}, false
}
