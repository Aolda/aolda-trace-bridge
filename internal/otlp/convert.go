package otlp

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aolda/aolda-trace-bridge/internal/redaction"
	"github.com/aolda/aolda-trace-bridge/internal/report"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

type Options struct {
	BaseID      string
	ServiceName string
	Redaction   redaction.Options
}

type Result struct {
	Request   *collectortrace.ExportTraceServiceRequest
	SpanCount int
}

func Convert(r report.Report, opts Options) (Result, error) {
	traceID, err := TraceIDFromBaseID(opts.BaseID)
	if err != nil {
		return Result{}, err
	}
	serviceName := opts.ServiceName
	if serviceName == "" {
		serviceName = "osprofiler-bridge"
	}

	traceStart := earliestRawTimestamp(r.Children)
	rootSpanID := SpanIDFromString(opts.BaseID)

	var spans []*tracepb.Span
	for i, child := range r.SpanNodes() {
		path := fmt.Sprintf("%d", i)
		spans = append(spans, convertNode(child, opts, traceID, rootSpanID, path, traceStart)...)
	}

	if len(spans) > 0 {
		root := rootSpan(r, opts, traceID, rootSpanID, spans)
		spans = append([]*tracepb.Span{root}, spans...)
	}

	req := &collectortrace.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						stringKV("service.name", serviceName),
					},
				},
				ScopeSpans: []*tracepb.ScopeSpans{
					{
						Scope: &commonpb.InstrumentationScope{
							Name:    "osprofiler-tempo-bridge",
							Version: "0.1.0",
						},
						Spans: spans,
					},
				},
			},
		},
	}

	return Result{Request: req, SpanCount: len(spans)}, nil
}

func convertNode(node report.Node, opts Options, traceID []byte, treeParentSpanID []byte, path string, traceStart time.Time) []*tracepb.Span {
	spanKey := firstNonEmpty(node.TraceID, report.InfoString(node.Info, "id"), path)
	spanID := SpanIDFromString(spanKey)

	var parentSpanID []byte
	if node.ParentID == opts.BaseID && len(treeParentSpanID) > 0 {
		parentSpanID = treeParentSpanID
	} else if node.ParentID != "" && node.ParentID != opts.BaseID {
		parentSpanID = SpanIDFromString(node.ParentID)
	} else if node.ParentID == "" && len(treeParentSpanID) > 0 {
		parentSpanID = treeParentSpanID
	}

	start, end := nodeTimes(node, traceStart)
	if end < start {
		end = start
	}

	span := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            spanID,
		ParentSpanId:      parentSpanID,
		Name:              spanName(node),
		Kind:              tracepb.Span_SPAN_KIND_INTERNAL,
		StartTimeUnixNano: uint64(start),
		EndTimeUnixNano:   uint64(end),
		Attributes:        spanAttributes(opts.BaseID, node, opts.Redaction),
	}

	if exception := report.InfoString(node.Info, "exception"); exception != "" && exception != "None" {
		span.Status = &tracepb.Status{
			Code:    tracepb.Status_STATUS_CODE_ERROR,
			Message: exception,
		}
	}

	spans := []*tracepb.Span{span}
	for i, child := range node.Children {
		childPath := path + "." + fmt.Sprintf("%d", i)
		spans = append(spans, convertNode(child, opts, traceID, spanID, childPath, traceStart)...)
	}
	return spans
}

func rootSpan(r report.Report, opts Options, traceID []byte, spanID []byte, children []*tracepb.Span) *tracepb.Span {
	start, end := spanBounds(children)
	if start == 0 || end == 0 {
		fallbackStart := start
		if fallbackStart == 0 {
			fallbackStart = end
		}
		reportStart, reportEnd := reportTimes(r.Info, fallbackStart)
		if start == 0 {
			start = reportStart
		}
		if end == 0 {
			end = reportEnd
		}
	}
	if end < start {
		end = start
	}

	name := report.InfoString(r.Info, "name")
	if name == "" || name == "total" {
		name = "osprofiler.total"
	}

	return &tracepb.Span{
		TraceId:           traceID,
		SpanId:            spanID,
		Name:              name,
		Kind:              tracepb.Span_SPAN_KIND_INTERNAL,
		StartTimeUnixNano: start,
		EndTimeUnixNano:   end,
		Attributes:        rootAttributes(opts.BaseID, r, opts.Redaction),
	}
}

func spanBounds(spans []*tracepb.Span) (uint64, uint64) {
	var start uint64
	var end uint64
	for _, span := range spans {
		if span.StartTimeUnixNano != 0 && (start == 0 || span.StartTimeUnixNano < start) {
			start = span.StartTimeUnixNano
		}
		if span.EndTimeUnixNano > end {
			end = span.EndTimeUnixNano
		}
	}
	return start, end
}

func reportTimes(info map[string]any, fallbackStart uint64) (uint64, uint64) {
	if fallbackStart == 0 {
		return 0, 0
	}
	startOffset, hasStart := report.InfoFloat(info, "started")
	endOffset, hasEnd := report.InfoFloat(info, "finished")
	if !hasStart || !hasEnd {
		return fallbackStart, fallbackStart
	}
	base := time.Unix(0, int64(fallbackStart)).Add(-time.Duration(startOffset * float64(time.Millisecond)))
	start := base.Add(time.Duration(startOffset * float64(time.Millisecond)))
	end := base.Add(time.Duration(endOffset * float64(time.Millisecond)))
	return uint64(start.UnixNano()), uint64(end.UnixNano())
}

func spanName(node report.Node) string {
	if name := report.InfoString(node.Info, "name"); name != "" {
		return name
	}
	return "osprofiler.span"
}

func nodeTimes(node report.Node, traceStart time.Time) (uint64, uint64) {
	name := spanName(node)
	startTime, startOK := rawPayloadTimestamp(node.Info, name+"-start")
	endTime, endOK := rawPayloadTimestamp(node.Info, name+"-stop")
	if startOK && endOK {
		return uint64(startTime.UnixNano()), uint64(endTime.UnixNano())
	}

	if !traceStart.IsZero() {
		startOffset, hasStart := report.InfoFloat(node.Info, "started")
		endOffset, hasEnd := report.InfoFloat(node.Info, "finished")
		if hasStart && hasEnd {
			start := traceStart.Add(time.Duration(startOffset * float64(time.Millisecond)))
			end := traceStart.Add(time.Duration(endOffset * float64(time.Millisecond)))
			return uint64(start.UnixNano()), uint64(end.UnixNano())
		}
	}

	if startOK {
		nano := uint64(startTime.UnixNano())
		return nano, nano
	}
	if endOK {
		nano := uint64(endTime.UnixNano())
		return nano, nano
	}
	return 0, 0
}

func rawPayloadTimestamp(info map[string]any, payloadName string) (time.Time, bool) {
	if info == nil {
		return time.Time{}, false
	}
	key := "meta.raw_payload." + payloadName
	value, ok := info[key]
	if !ok {
		return time.Time{}, false
	}
	payload, ok := value.(map[string]any)
	if !ok {
		return time.Time{}, false
	}
	timestamp, ok := payload["timestamp"].(string)
	if !ok || timestamp == "" {
		return time.Time{}, false
	}
	parsed, err := ParseTimestamp(timestamp)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

func earliestRawTimestamp(nodes []report.Node) time.Time {
	var earliest time.Time
	var walk func([]report.Node)
	walk = func(items []report.Node) {
		for _, node := range items {
			for key, value := range node.Info {
				if !strings.HasPrefix(key, "meta.raw_payload.") {
					continue
				}
				payload, ok := value.(map[string]any)
				if !ok {
					continue
				}
				raw, _ := payload["timestamp"].(string)
				if raw == "" {
					continue
				}
				parsed, err := ParseTimestamp(raw)
				if err != nil {
					continue
				}
				if earliest.IsZero() || parsed.Before(earliest) {
					earliest = parsed
				}
			}
			walk(node.Children)
		}
	}
	walk(nodes)
	return earliest
}

func spanAttributes(baseID string, node report.Node, opts redaction.Options) []*commonpb.KeyValue {
	attrs := []*commonpb.KeyValue{
		stringKV("osprofiler.base_id", baseID),
	}
	if node.TraceID != "" {
		attrs = append(attrs, stringKV("osprofiler.trace_id", node.TraceID))
	}
	if node.ParentID != "" {
		attrs = append(attrs, stringKV("osprofiler.parent_id", node.ParentID))
	}
	for _, key := range []string{"project", "service", "host"} {
		if value := report.InfoString(node.Info, key); value != "" {
			attrs = append(attrs, stringKV("osprofiler."+key, value))
		}
	}

	redacted := redaction.Redact(node.Info, opts)
	data, err := json.Marshal(redacted)
	if err == nil {
		attrs = append(attrs, stringKV("osprofiler.info_json", string(data)))
	}
	return attrs
}

func rootAttributes(baseID string, r report.Report, opts redaction.Options) []*commonpb.KeyValue {
	attrs := []*commonpb.KeyValue{
		stringKV("osprofiler.base_id", baseID),
		stringKV("osprofiler.synthetic_root", "true"),
	}

	redacted := redaction.Redact(r.Info, opts)
	data, err := json.Marshal(redacted)
	if err == nil {
		attrs = append(attrs, stringKV("osprofiler.info_json", string(data)))
	}
	if len(r.Stats) > 0 {
		data, err := json.Marshal(r.Stats)
		if err == nil {
			attrs = append(attrs, stringKV("osprofiler.stats_json", string(data)))
		}
	}
	return attrs
}

func stringKV(key, value string) *commonpb.KeyValue {
	return &commonpb.KeyValue{
		Key: key,
		Value: &commonpb.AnyValue{
			Value: &commonpb.AnyValue_StringValue{StringValue: value},
		},
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
