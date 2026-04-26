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
	if len(resourceSpans) != 3 {
		t.Fatalf("resource spans len = %d, want 3", len(resourceSpans))
	}

	var spans []*tracepb.Span
	spanResourceAttrs := map[*tracepb.Span][]*commonpb.KeyValue{}
	for _, rs := range resourceSpans {
		resourceAttrs := rs.Resource.Attributes
		if got := attr(resourceAttrs, "service.name"); got != "keystone" {
			t.Fatalf("resource service.name = %q, want keystone", got)
		}
		if got := attr(resourceAttrs, "service.namespace"); got != "openstack" {
			t.Fatalf("resource service.namespace = %q, want openstack", got)
		}
		for _, scope := range rs.ScopeSpans {
			for _, span := range scope.Spans {
				spans = append(spans, span)
				spanResourceAttrs[span] = resourceAttrs
			}
		}
	}
	if len(spans) != result.SpanCount {
		t.Fatalf("collected spans = %d, want %d", len(spans), result.SpanCount)
	}

	root := spanByName(spans, "osprofiler.total")
	if root == nil {
		t.Fatal("missing synthetic root span")
	}
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
	if got := attr(spanResourceAttrs[root], "service.instance.id"); got != "" {
		t.Fatalf("root service.instance.id = %q, want empty because trace spans multiple hosts", got)
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
	if computeWSGI.Name != "keystone.wsgi POST /v3/auth/tokens" {
		t.Fatalf("compute wsgi name = %q", computeWSGI.Name)
	}
	if got := attr(spanResourceAttrs[computeWSGI], "service.instance.id"); got != "aolda-compute" {
		t.Fatalf("compute wsgi service.instance.id = %q, want aolda-compute", got)
	}
	if !bytes.Equal(computeWSGI.ParentSpanId, root.SpanId) {
		t.Fatalf("compute wsgi parent = %x, want root %x", computeWSGI.ParentSpanId, root.SpanId)
	}
	storageWSGI := byOSProfilerTraceID["220a5259-7225-4098-9b5d-6aa857ca297b"]
	if storageWSGI == nil {
		t.Fatal("missing storage wsgi span")
	}
	if storageWSGI.Name != "keystone.wsgi GET /" {
		t.Fatalf("storage wsgi name = %q", storageWSGI.Name)
	}
	if got := attr(spanResourceAttrs[storageWSGI], "service.instance.id"); got != "aolda-storage" {
		t.Fatalf("storage wsgi service.instance.id = %q, want aolda-storage", got)
	}
	dbSpan := byOSProfilerTraceID["ca5fcf6c-4c07-4d83-88d2-cbecf4c22c6d"]
	if dbSpan == nil {
		t.Fatal("missing db span")
	}
	if dbSpan.Name != "keystone.db SQL" {
		t.Fatalf("db span name = %q", dbSpan.Name)
	}
	if got := attr(spanResourceAttrs[dbSpan], "service.instance.id"); got != "aolda-compute" {
		t.Fatalf("db service.instance.id = %q, want aolda-compute", got)
	}
	if !bytes.Equal(dbSpan.ParentSpanId, computeWSGI.SpanId) {
		t.Fatalf("db parent = %x, want compute wsgi %x", dbSpan.ParentSpanId, computeWSGI.SpanId)
	}
}

func TestSQLStatementSummary(t *testing.T) {
	tests := map[string]string{
		"SELECT user.id AS user_id FROM user WHERE user.id = %(pk_1)s": "SELECT user",
		"SELECT role_option.role_id FROM (SELECT role.id AS role_id FROM role WHERE role.id = %(pk_1)s) AS anon_1 INNER JOIN role_option ON anon_1.role_id = role_option.role_id": "SELECT role",
		"INSERT INTO token (id) VALUES (%(id)s)":      "INSERT token",
		"UPDATE public.user SET enabled = true":       "UPDATE user",
		"DELETE FROM service_provider WHERE id = 'x'": "DELETE service_provider",
		"<redacted sql>": "SQL",
	}
	for statement, want := range tests {
		if got := sqlStatementSummary(statement); got != want {
			t.Fatalf("sqlStatementSummary(%q) = %q, want %q", statement, got, want)
		}
	}
}

func spanByName(spans []*tracepb.Span, name string) *tracepb.Span {
	for _, span := range spans {
		if span.Name == name {
			return span
		}
	}
	return nil
}

func attr(attrs []*commonpb.KeyValue, key string) string {
	for _, kv := range attrs {
		if kv.Key == key {
			return kv.Value.GetStringValue()
		}
	}
	return ""
}
