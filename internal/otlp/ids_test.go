package otlp

import (
	"encoding/hex"
	"testing"
)

func TestTraceIDFromUUIDBaseID(t *testing.T) {
	got, err := TraceIDFromBaseID("251bb5c1-30fb-4b04-a223-4410b831d4d7")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 16 {
		t.Fatalf("trace id len = %d", len(got))
	}
	if hex.EncodeToString(got) != "251bb5c130fb4b04a2234410b831d4d7" {
		t.Fatalf("unexpected trace id: %x", got)
	}
}

func TestSpanIDFromStringStable(t *testing.T) {
	first := SpanIDFromString("149beffd-1985-4d29-ba30-bd597ed5a293")
	second := SpanIDFromString("149beffd-1985-4d29-ba30-bd597ed5a293")
	if len(first) != 8 {
		t.Fatalf("span id len = %d", len(first))
	}
	if hex.EncodeToString(first) != hex.EncodeToString(second) {
		t.Fatal("span id not stable")
	}
}
