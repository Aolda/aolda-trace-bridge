package otlp

import (
	"fmt"
	"time"
)

var timestampLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.999999",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05.999999",
	"2006-01-02 15:04:05.999999999",
}

func ParseTimestamp(value string) (time.Time, error) {
	var lastErr error
	for _, layout := range timestampLayouts {
		t, err := time.Parse(layout, value)
		if err == nil {
			if t.Location() == time.Local {
				return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC), nil
			}
			return t.UTC(), nil
		}
		lastErr = err
	}
	return time.Time{}, fmt.Errorf("parse timestamp %q: %w", value, lastErr)
}
