package otlp

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/aolda/aolda-trace-bridge/internal/redaction"
	"github.com/aolda/aolda-trace-bridge/internal/report"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
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
	if result.SpanCount != 6 {
		t.Fatalf("span count = %d, want 6", result.SpanCount)
	}

	resourceSpans := result.Request.ResourceSpans
	if len(resourceSpans) != 1 {
		t.Fatalf("resource spans len = %d", len(resourceSpans))
	}
	if got := attr(resourceSpans[0].Resource.Attributes, "service.name"); got != "osprofiler-bridge" {
		t.Fatalf("service.name = %q", got)
	}

	spans := resourceSpans[0].ScopeSpans[0].Spans
	root := spans[0]
	if root.Name != "osprofiler.total" {
		t.Fatalf("root span name = %q, want osprofiler.total", root.Name)
	}
	if len(root.ParentSpanId) != 0 {
		t.Fatalf("root span has parent span id: %x", root.ParentSpanId)
	}
	if !bytes.Equal(root.SpanId, SpanIDFromString("251bb5c1-30fb-4b04-a223-4410b831d4d7")) {
		t.Fatalf("root span id = %x, want hash of base_id", root.SpanId)
	}
	if got := attr(root.Attributes, "osprofiler.synthetic_root"); got != "true" {
		t.Fatalf("root synthetic attribute = %q, want true", got)
	}

	var sawChildOfRoot bool
	byOSProfilerTraceID := map[string]*tracepb.Span{}
	for _, span := range spans {
		if hex.EncodeToString(span.TraceId) != "251bb5c130fb4b04a2234410b831d4d7" {
			t.Fatalf("unexpected trace id for span %s: %x", span.Name, span.TraceId)
		}
		if span.StartTimeUnixNano == 0 || span.EndTimeUnixNano == 0 {
			t.Fatalf("span %s has zero timestamps", span.Name)
		}
		if span.EndTimeUnixNano < span.StartTimeUnixNano {
			t.Fatalf("span %s end before start", span.Name)
		}
		if span != root {
			if span.StartTimeUnixNano < root.StartTimeUnixNano {
				t.Fatalf("span %s starts before root", span.Name)
			}
			if span.EndTimeUnixNano > root.EndTimeUnixNano {
				t.Fatalf("span %s ends after root", span.Name)
			}
		}
		if attr(span.Attributes, "osprofiler.parent_id") == "251bb5c1-30fb-4b04-a223-4410b831d4d7" &&
			bytes.Equal(span.ParentSpanId, root.SpanId) {
			sawChildOfRoot = true
		}
		if osprofilerTraceID := attr(span.Attributes, "osprofiler.trace_id"); osprofilerTraceID != "" {
			byOSProfilerTraceID[osprofilerTraceID] = span
		}
		infoJSON := attr(span.Attributes, "osprofiler.info_json")
		for _, leaked := range []string{"pk_1"} {
			if strings.Contains(infoJSON, leaked) {
				t.Fatalf("span %s leaked %q in info_json", span.Name, leaked)
			}
		}
	}
	if !sawChildOfRoot {
		t.Fatal("no span with OSProfiler parent_id=base_id was attached to synthetic root")
	}

	computeWSGI := byOSProfilerTraceID["149beffd-1985-4d29-ba30-bd597ed5a293"]
	if computeWSGI == nil {
		t.Fatal("missing compute wsgi span")
	}
	if !bytes.Equal(computeWSGI.ParentSpanId, root.SpanId) {
		t.Fatalf("compute wsgi parent = %x, want root %x", computeWSGI.ParentSpanId, root.SpanId)
	}
	dbSpan := byOSProfilerTraceID["ca5fcf6c-4c07-4d83-88d2-cbecf4c22c6d"]
	if dbSpan == nil {
		t.Fatal("missing db span")
	}
	if !bytes.Equal(dbSpan.ParentSpanId, computeWSGI.SpanId) {
		t.Fatalf("db parent = %x, want compute wsgi %x", dbSpan.ParentSpanId, computeWSGI.SpanId)
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
