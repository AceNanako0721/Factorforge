package domain

import "time"

type TimeWindow struct {
	ID              string    `json:"window_id"`
	CalendarVersion string    `json:"calendar_version"`
	Kind            string    `json:"kind"`
	Start           time.Time `json:"start"`
	End             time.Time `json:"end"`
}
