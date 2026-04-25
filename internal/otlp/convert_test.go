package otlp

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/aolda/aolda-trace-bridge/internal/redaction"
	"github.com/aolda/aolda-trace-bridge/internal/report"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
)

func TestConvertFixtureToOTLP(t *testing.T) {
	rep, err := report.LoadFile("../../docs/samples/osprofiler_trace_show_251bb5c1_redacted.json")
	if err != nil {
		t.Fatal(err)
	}

	result, err := Convert(rep, Options{
		BaseID:      "251bb5c1-30fb-4b04-a223-4410b831d4d7",
		ServiceName: "osprofiler-bridge",
		Redaction: redaction.Options{
			RedactDBParams:      true,
			RedactDBStatement:   false,
			RedactSensitiveKeys: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.SpanCount != 5 {
		t.Fatalf("span count = %d, want 5", result.SpanCount)
	}

	resourceSpans := result.Request.ResourceSpans
	if len(resourceSpans) != 1 {
		t.Fatalf("resource spans len = %d", len(resourceSpans))
	}
	if got := attr(resourceSpans[0].Resource.Attributes, "service.name"); got != "osprofiler-bridge" {
		t.Fatalf("service.name = %q", got)
	}

	spans := resourceSpans[0].ScopeSpans[0].Spans
	for _, span := range spans {
		if span.Name == "total" {
			t.Fatal("top-level total container should not be exported as span")
		}
		if hex.EncodeToString(span.TraceId) != "251bb5c130fb4b04a2234410b831d4d7" {
			t.Fatalf("unexpected trace id for span %s: %x", span.Name, span.TraceId)
		}
		if span.StartTimeUnixNano == 0 || span.EndTimeUnixNano == 0 {
			t.Fatalf("span %s has zero timestamps", span.Name)
		}
		if span.EndTimeUnixNano < span.StartTimeUnixNano {
			t.Fatalf("span %s end before start", span.Name)
		}
		infoJSON := attr(span.Attributes, "osprofiler.info_json")
		for _, leaked := range []string{"pk_1"} {
			if strings.Contains(infoJSON, leaked) {
				t.Fatalf("span %s leaked %q in info_json", span.Name, leaked)
			}
		}
	}
}

func attr(attrs []*commonpb.KeyValue, key string) string {
	for _, kv := range attrs {
		if kv.Key == key {
			return kv.Value.GetStringValue()
		}
	}
	return ""
}
