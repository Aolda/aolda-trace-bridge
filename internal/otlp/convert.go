package otlp

import (
	"encoding/json"
	"fmt"
	"regexp"
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
		ResourceSpans: resourceSpans(spans, serviceName),
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
		Name:              displaySpanName(node),
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

func resourceSpans(spans []*tracepb.Span, fallbackServiceName string) []*tracepb.ResourceSpans {
	commonProject := commonSpanAttr(spans, "osprofiler.project")
	commonHost := commonSpanAttr(spans, "osprofiler.host")

	type resourceGroup struct {
		project string
		host    string
		spans   []*tracepb.Span
	}

	var order []string
	groups := map[string]*resourceGroup{}
	for _, span := range spans {
		project := attrValue(span.Attributes, "osprofiler.project")
		host := attrValue(span.Attributes, "osprofiler.host")
		if attrValue(span.Attributes, "osprofiler.synthetic_root") == "true" {
			project = commonProject
			host = commonHost
		}
		key := project + "\x00" + host
		group, ok := groups[key]
		if !ok {
			group = &resourceGroup{project: project, host: host}
			groups[key] = group
			order = append(order, key)
		}
		group.spans = append(group.spans, span)
	}

	var out []*tracepb.ResourceSpans
	for _, key := range order {
		group := groups[key]
		serviceName := group.project
		if serviceName == "" {
			serviceName = fallbackServiceName
		}

		attrs := []*commonpb.KeyValue{
			stringKV("service.name", serviceName),
			stringKV("service.namespace", "openstack"),
		}
		if group.host != "" {
			attrs = append(attrs, stringKV("service.instance.id", group.host))
			attrs = append(attrs, stringKV("host.name", group.host))
		}

		out = append(out, &tracepb.ResourceSpans{
			Resource: &resourcepb.Resource{
				Attributes: attrs,
			},
			ScopeSpans: []*tracepb.ScopeSpans{
				{
					Scope: &commonpb.InstrumentationScope{
						Name:    "osprofiler-tempo-bridge",
						Version: "0.1.0",
					},
					Spans: group.spans,
				},
			},
		})
	}
	return out
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

func osprofilerOperationName(node report.Node) string {
	if name := report.InfoString(node.Info, "name"); name != "" {
		return name
	}
	return "osprofiler.span"
}

func displaySpanName(node report.Node) string {
	operation := osprofilerOperationName(node)
	project := report.InfoString(node.Info, "project")
	prefix := operation
	if project != "" {
		prefix = project + "." + operation
	}

	switch operation {
	case "wsgi":
		if method, path := httpRequestSummary(node.Info, operation); method != "" && path != "" {
			return prefix + " " + method + " " + path
		}
	case "db":
		if summary := dbStatementSummary(node.Info, operation); summary != "" {
			return prefix + " " + summary
		}
	}
	return prefix
}

func nodeTimes(node report.Node, traceStart time.Time) (uint64, uint64) {
	name := osprofilerOperationName(node)
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

func httpRequestSummary(info map[string]any, operation string) (string, string) {
	payload, ok := rawPayload(info, operation+"-start")
	if !ok {
		return "", ""
	}
	payloadInfo, ok := payload["info"].(map[string]any)
	if !ok {
		return "", ""
	}
	request, ok := payloadInfo["request"].(map[string]any)
	if !ok {
		return "", ""
	}
	method, _ := request["method"].(string)
	path, _ := request["path"].(string)
	return strings.ToUpper(method), path
}

func dbStatementSummary(info map[string]any, operation string) string {
	payload, ok := rawPayload(info, operation+"-start")
	if !ok {
		return ""
	}
	payloadInfo, ok := payload["info"].(map[string]any)
	if !ok {
		return ""
	}
	db, ok := payloadInfo["db"].(map[string]any)
	if !ok {
		return ""
	}
	statement, _ := db["statement"].(string)
	return sqlStatementSummary(statement)
}

var (
	sqlFromRE       = regexp.MustCompile(`(?i)\bFROM\s+([A-Za-z_][A-Za-z0-9_.$]*)`)
	sqlIntoRE       = regexp.MustCompile(`(?i)\bINTO\s+([A-Za-z_][A-Za-z0-9_.$]*)`)
	sqlUpdateRE     = regexp.MustCompile(`(?i)\bUPDATE\s+([A-Za-z_][A-Za-z0-9_.$]*)`)
	sqlOperationRE  = regexp.MustCompile(`(?i)^\s*([A-Z]+)\b`)
	identifierTrim  = strings.NewReplacer(`"`, "", "`", "", "[", "", "]", "")
	whitespaceRunRE = regexp.MustCompile(`\s+`)
)

func sqlStatementSummary(statement string) string {
	statement = whitespaceRunRE.ReplaceAllString(strings.TrimSpace(statement), " ")
	if statement == "" {
		return ""
	}

	operation := "SQL"
	if match := sqlOperationRE.FindStringSubmatch(statement); len(match) == 2 {
		operation = strings.ToUpper(match[1])
	}

	table := ""
	switch operation {
	case "SELECT", "DELETE":
		table = firstSQLIdentifier(statement, sqlFromRE)
	case "INSERT":
		table = firstSQLIdentifier(statement, sqlIntoRE)
	case "UPDATE":
		table = firstSQLIdentifier(statement, sqlUpdateRE)
	}
	if table == "" {
		return operation
	}
	return operation + " " + table
}

func firstSQLIdentifier(statement string, re *regexp.Regexp) string {
	match := re.FindStringSubmatch(statement)
	if len(match) != 2 {
		return ""
	}
	identifier := identifierTrim.Replace(match[1])
	identifier = strings.Trim(identifier, " ,;()")
	if dot := strings.LastIndex(identifier, "."); dot >= 0 && dot < len(identifier)-1 {
		identifier = identifier[dot+1:]
	}
	return identifier
}

func rawPayloadTimestamp(info map[string]any, payloadName string) (time.Time, bool) {
	payload, ok := rawPayload(info, payloadName)
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

func rawPayload(info map[string]any, payloadName string) (map[string]any, bool) {
	if info == nil {
		return nil, false
	}
	key := "meta.raw_payload." + payloadName
	value, ok := info[key]
	if !ok {
		return nil, false
	}
	payload, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	return payload, true
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

func commonSpanAttr(spans []*tracepb.Span, key string) string {
	var common string
	for _, span := range spans {
		if attrValue(span.Attributes, "osprofiler.synthetic_root") == "true" {
			continue
		}
		value := attrValue(span.Attributes, key)
		if value == "" {
			continue
		}
		if common == "" {
			common = value
			continue
		}
		if common != value {
			return ""
		}
	}
	return common
}

func attrValue(attrs []*commonpb.KeyValue, key string) string {
	for _, kv := range attrs {
		if kv.Key == key {
			return kv.Value.GetStringValue()
		}
	}
	return ""
}
