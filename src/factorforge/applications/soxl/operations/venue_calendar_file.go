package operations

import (
	"encoding/json"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/domain"
)

func CompileVenueCalendarFile(root, input, output string, maxBytes int, now time.Time) error {
	return transformFile(root, input, output, maxBytes, func(raw []byte) ([]byte, error) {
		var request VenueCalendarRequest
		if d.DecodePrivate(raw, &request) != nil {
			return nil, d.Fail("VENUE_CALENDAR_INVALID", 422)
		}
		artifact, err := CompileVenueCalendar(request, now)
		if err != nil {
			return nil, err
		}
		return json.MarshalIndent(artifact, "", "  ")
	})
}
