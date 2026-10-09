package monitoring

import "time"

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
	return time.Time{}, false
}
