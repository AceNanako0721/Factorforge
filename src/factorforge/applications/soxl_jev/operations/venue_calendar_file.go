package operations

import (
	"encoding/json"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
)

func CompileVenueCalendarFile(root, input, output string, maxBytes int, now time.Time) error {
	return transformEvidenceFile(root, input, output, maxBytes, func(raw []byte) ([]byte, error) {
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

func LoadVenueCalendar(root, path string, binding d.Binding, now time.Time, maxBytes int) (VenueCalendarArtifact, error) {
	var a VenueCalendarArtifact
	raw, err := readPrivateArtifact(root, path, maxBytes)
	if err != nil || d.DecodePrivate(raw, &a) != nil || ValidateVenueCalendar(a, binding, now) != nil {
		return VenueCalendarArtifact{}, d.Fail("VENUE_CALENDAR_ARTIFACT_INVALID", 422)
	}
	return a, nil
}
