# Architecture Notes

## Planning Guardrail

This document is a planning artifact. Do not implement code from it until the owner explicitly confirms that planning is complete.

## Architectural Decision

Approved direction as of 2026-04-26:

```text
Do not build a Go Redis raw event parser for MVP.
Use OSProfiler report JSON as the intermediate format.
Use a long-running Python helper as the OSProfiler driver adapter.
Use Go for process control, JSON-to-OTLP conversion, export, metrics, and future state handling.
```

## Important Source Findings

The OSProfiler Redis driver writes each event as JSON into a Redis list:

```text
LPUSH osprofiler_opt:<base_id> <json-event>
```

The same driver also exposes `get_report(base_id)`, which reads stored events and builds a report tree. OSProfiler CLI `trace show --json --connection-string=<URI>` uses the storage driver and prints that report JSON.

Implication:

```text
Redis raw event parsing exists, but the bridge should delegate it to OSProfiler's Python code.
```

References:

- https://docs.openstack.org/osprofiler/latest/_modules/osprofiler/drivers/redis_driver.html
- https://docs.openstack.org/osprofiler/latest/_modules/osprofiler/cmd/commands.html
- https://docs.openstack.org/osprofiler/latest/user/api.html

## Data Flow

MVP flow:

```text
operator supplies base_id
  -> Go bridge
  -> Python helper subprocess over stdin/stdout NDJSON
  -> OSProfiler storage driver get_report(base_id)
  -> OSProfiler report JSON
  -> Go JSON-to-OTLP converter
  -> OTLP HTTP exporter
  -> OTel Collector
  -> Tempo
```

Future automated flow:

```text
Go bridge scheduler
  -> helper list_traces()
  -> per-trace get_report(base_id)
  -> Redis-backed done/state handling
  -> OTLP export
```

## Component Boundaries

### Python Helper

The helper is intentionally narrow.

Allowed responsibilities:

- Import OSProfiler Python libraries.
- Initialize the OSProfiler storage driver.
- Call `engine.get_report(base_id)`.
- Later, call `engine.list_traces()` when automatic discovery is added.
- Serialize report JSON or structured errors to stdout.

Disallowed responsibilities:

- OTLP conversion.
- Tempo or Collector export.
- Retry policy.
- Export state.
- Done markers.
- Redis raw key scanning outside OSProfiler driver APIs.

### Go Bridge

The Go process owns the bridge behavior.

Responsibilities:

- Config loading.
- Python helper lifecycle.
- NDJSON request/response protocol.
- Request timeouts and helper failure policy.
- Report JSON validation.
- ID conversion.
- Timestamp conversion.
- Redaction.
- OTLP protobuf generation.
- OTLP HTTP export.
- Metrics and health endpoints.
- Future polling, done markers, and retries.

## Helper Protocol

Use newline-delimited JSON over stdin/stdout for the first implementation.

Request:

```json
{"id":"1","method":"get_report","base_id":"8dbf..."}
```

Success:

```json
{"id":"1","ok":true,"report":{}}
```

Failure:

```json
{"id":"1","ok":false,"error":{"code":"trace_not_found","message":"trace not found"}}
```

Protocol rules:

- Each message is one JSON object followed by `\n`.
- `id` is required and echoed back.
- Unknown methods return `unknown_method`.
- Malformed JSON returns `bad_request` if possible, otherwise the helper may exit.
- Helper stderr is reserved for diagnostic logs and must not be parsed as protocol output.
- If the helper exits, times out, or emits malformed protocol output, the MVP bridge fails the current export and exits with a clear error. It does not restart the helper automatically.

## Report JSON Shape

A sanitized real `osprofiler trace show --json` sample from the target environment is stored at:

```text
docs/samples/osprofiler_trace_show_251bb5c1_redacted.json
```

The converter should still use a defensive struct:

```text
node.info: map[string]any
node.children: []node
selected known fields extracted opportunistically
unknown fields preserved inside osprofiler.info_json
```

Observed report shape:

- Top-level `info.name` is `total`.
- Top-level `info` has `started`, `finished`, and `last_trace_started`, but no `trace_id`.
- Real spans are in `children`.
- Each span node has top-level `trace_id`, `parent_id`, and `children`.
- Each span node has `info.name`, `info.project`, `info.service`, `info.host`, `info.started`, `info.finished`, and `info.exception`.
- Raw OSProfiler start/stop payloads are nested under keys like `info["meta.raw_payload.wsgi-start"]` and `info["meta.raw_payload.db-stop"]`.
- `stats` summarizes span kind counts and durations.

Implementation implication:

```text
Export top-level "total" as a synthetic root span named "osprofiler.total" so Grafana can render one coherent trace tree.
```

## Observed Redis Raw Event Sample

A sanitized Redis `LRANGE osprofiler_opt:<base_id> 0 -1` sample from the target environment is stored at:

```text
docs/samples/redis_lrange_251bb5c1_redacted.ndjson
```

This sample is not the MVP input format, but it confirms important source behavior:

- Redis list order is newest first because OSProfiler uses `LPUSH`.
- Top-level fields include `name`, `base_id`, `trace_id`, `parent_id`, `timestamp`, `project`, and `service`.
- `project=keystone` and `service=public` were observed as top-level fields.
- `info.host` was present on both start and stop events.
- WSGI request details appeared on `wsgi-start` under `info.request`.
- DB SQL details appeared on `db-start` under `info.db`.
- DB stop events generally contained only `info.host`.
- Root-ish WSGI spans used `parent_id = base_id`.
- Multiple WSGI spans can share the same `base_id` and have different `trace_id` values.

Implementation implication:

```text
The helper/get_report path remains the MVP source of truth.
The raw sample should be used only as source evidence and redaction test material.
```

## ID Conversion

OTLP expects fixed-length binary identifiers:

```text
trace_id: 16 bytes
span_id: 8 bytes
```

MVP rule:

- `base_id`: parse UUID and use raw 16 bytes as OTLP trace ID.
- non-UUID `base_id`: use a stable 128-bit hash.
- synthetic root span: use stable 64-bit hash of `base_id` as span ID.
- node `trace_id` or `id`: use stable 64-bit hash as span ID.
- `parent_id`: use stable 64-bit hash if present.
- if `parent_id == base_id`, attach the span to the synthetic root span.
- if `parent_id` is absent, derive parent span ID from the report tree parent.
- synthetic root span has no parent span ID.

The hash algorithm must be deterministic across process restarts and bridge versions unless a documented migration is made.

## Timestamp Conversion

Preferred fields for real report nodes:

```text
info["meta.raw_payload.<name>-start"].timestamp -> span start_time
info["meta.raw_payload.<name>-stop"].timestamp  -> span end_time
```

Fallbacks:

```text
trace_start_absolute + info.started milliseconds
trace_start_absolute + info.finished milliseconds
timestamp for both start and end
```

In the observed OSProfiler report JSON, `info.started` and `info.finished` are relative millisecond offsets from the trace start. They are not Unix timestamps.

Timezone-less timestamps should be treated as UTC to avoid deployment-dependent behavior.

If only one timestamp exists, emit a best-effort span with zero or minimal duration and increment a metric once metrics exist.

## Resource Grouping

Resource attributes are derived from OSProfiler span metadata:

```text
service.name = <project>, for example "keystone"
service.namespace = "openstack"
service.instance.id = <host>, for example "aolda-compute"
host.name = <host>
```

Spans are grouped into separate OTLP `ResourceSpans` when the host differs. The synthetic root span uses the common project when all child spans share one project, and omits `service.instance.id` when the trace spans multiple hosts. If project metadata is missing, `bridge.service_name` is used as the fallback `service.name`.

## Span Attributes

MVP attributes:

```text
osprofiler.info_json
osprofiler.base_id
osprofiler.trace_id
osprofiler.parent_id
```

`osprofiler.info_json` should contain a redacted JSON string of the node `info` object. Unknown OSProfiler fields should not be thrown away in MVP.

Avoid high-cardinality metric labels such as raw path, SQL statement, base ID, trace ID, or full host+path combinations.

## Redaction

Redaction must run before:

- Logging.
- Metrics labels.
- OTLP attributes.

Default behavior:

```text
redact_db_params = true
redact_db_statement = false
redact_sensitive_keys = true
```

Sensitive key matching should cover at least:

```text
password
passwd
secret
token
auth
authorization
cookie
set-cookie
credential
```

## OTLP Export

Default endpoint type:

```text
OTLP HTTP protobuf
```

Default deployment target:

```text
http://otel-collector:4318/v1/traces
```

Tempo direct is allowed as a config override for local testing or simplified deployments, but the planned deployment path is OTel Collector first.

Request:

```text
POST <endpoint>
Content-Type: application/x-protobuf
body: opentelemetry.proto.collector.trace.v1.ExportTraceServiceRequest
```

Success criteria:

- Any 2xx response is a transport success.
- Non-2xx is a failure.
- Timeout is a failure.

MVP does not write done markers, so retry is manual by rerunning the same base ID.

## Container Deployment

The bridge should be built and run as a Docker container.

Image contents:

- Go bridge binary.
- Python helper script.
- Python runtime.
- `osprofiler` Python package.
- Redis/Valkey Python dependency required by OSProfiler.

Runtime inputs:

```text
--config /etc/osprofiler-tempo-bridge/config.yaml
OSPROFILER_CONNECTION_STRING
OTLP_ENDPOINT
```

CLI:

```text
osprofiler-tempo-bridge export --base-id <uuid> --config /etc/osprofiler-tempo-bridge/config.yaml
```

The config file should support environment expansion so secrets can be injected by Docker/Kubernetes environment or secret mechanisms. Logs must redact connection strings before printing config or error context.

## Metrics

Initial metrics after the first export path exists:

```text
bridge_exports_total
bridge_export_failures_total
bridge_spans_exported_total
bridge_helper_requests_total
bridge_helper_failures_total
bridge_helper_request_duration_seconds
bridge_otlp_request_duration_seconds
bridge_otlp_requests_total
```

Future automated metrics:

```text
bridge_traces_discovered_total
bridge_traces_skipped_done_total
bridge_trace_late_events_total
bridge_state_errors_total
```

## Deferred Architecture

The previous raw Redis design is deferred, not the MVP path.

Deferred topics:

- Redis `SCAN` for automatic trace discovery.
- Redis locks for multi-worker processing.
- Done marker state.
- Late-event detection.
- `LLEN` stability heuristics.
- Re-export policy.
- Redis key deletion.

These should be revisited only after the single-trace JSON-to-OTLP path is proven.
