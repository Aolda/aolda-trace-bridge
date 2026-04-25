package otlp

import "testing"

func TestParseTimestampTreatsTimezoneLessAsUTC(t *testing.T) {
	got, err := ParseTimestamp("2026-04-25T15:30:00.537327")
	if err != nil {
		t.Fatal(err)
	}
	if got.Location().String() != "UTC" {
		t.Fatalf("expected UTC, got %s", got.Location())
	}
	if got.UnixNano() == 0 {
		t.Fatal("expected non-zero timestamp")
	}
}
