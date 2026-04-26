# Implementation Plan

## Planning Guardrail

Do not start implementation until the owner explicitly says planning is complete.

This plan exists so future agents can proceed consistently once implementation is approved.

## Phase 0: Confirm Inputs

Approved direction:

```text
single base_id export
Go bridge + long-running Python helper
stdin/stdout NDJSON
default OTel Collector OTLP HTTP endpoint
actual OSProfiler JSON sample before final Go struct lock-in
```

Before writing production code, confirm:

- One real `osprofiler trace show --json` or `engine.get_report()` sample is available, or explicitly use a temporary fixture with a tracked follow-up.
- The target endpoint is an OTel Collector OTLP HTTP endpoint.
- The runtime image can include Python, `osprofiler`, and the Redis Python dependency.

## Phase 1: Project Skeleton

Expected shape after implementation approval:

```text
cmd/osprofiler-tempo-bridge/
internal/config/
internal/helper/
internal/report/
internal/otlp/
internal/redaction/
internal/exporter/
internal/metrics/
helper/osprofiler_helper.py
testdata/
```

Package boundaries:

- `config`: config loading, env expansion, duration parsing.
- `helper`: subprocess lifecycle, NDJSON protocol, timeouts.
- `report`: OSProfiler report JSON model and tree traversal.
- `otlp`: report-to-OTLP conversion.
- `redaction`: safe attribute redaction.
- `exporter`: OTLP HTTP exporter.
- `metrics`: Prometheus collectors and HTTP handler.
- `helper/osprofiler_helper.py`: thin OSProfiler driver adapter.

## Phase 2: Config

Support YAML config shaped like:

```yaml
osprofiler:
  connection_string: "${OSPROFILER_CONNECTION_STRING}"

helper:
  command:
    - "python3"
    - "helper/osprofiler_helper.py"
  request_timeout: "10s"
  startup_timeout: "5s"

otlp:
  endpoint: "${OTLP_ENDPOINT}"
  timeout: "5s"

bridge:
  service_name: "osprofiler-bridge"
  redact_db_params: true
  redact_db_statement: false
  redact_sensitive_keys: true

metrics:
  listen_addr: ":9090"
  path: "/metrics"
```

Validation rules:

- `osprofiler.connection_string` is required.
- `helper.command` is required.
- `helper.request_timeout` must parse successfully.
- `otlp.endpoint` is required.
- `otlp.timeout` must parse successfully.
- `bridge.service_name` defaults to `osprofiler-bridge`.
- Config file with environment expansion and a `--config` CLI flag is enough for MVP.

Required environment variables for the planned container deployment:

```text
OSPROFILER_CONNECTION_STRING=redis://:<password>@<redis-host>:6379/0
OTLP_ENDPOINT=http://otel-collector:4318/v1/traces
```

Do not print these values in logs.

## Phase 2.5: Container Packaging

Build a Docker image containing:

- Go bridge binary.
- `helper/osprofiler_helper.py`.
- Python runtime.
- `osprofiler` Python package.
- Redis/Valkey Python dependency required by OSProfiler.

The image should not contain environment-specific config or secrets.

The container should accept:

```text
--config /etc/osprofiler-tempo-bridge/config.yaml
```

and resolve environment placeholders at startup.

MVP CLI:

```text
osprofiler-tempo-bridge export --base-id <uuid> --config /etc/osprofiler-tempo-bridge/config.yaml
```

## Phase 3: Test Fixture and Report Model

Use the sanitized real OSProfiler report JSON fixture:

```text
docs/samples/osprofiler_trace_show_251bb5c1_redacted.json
```

If real data is unavailable:

- Create a temporary fixture that matches documented `trace show --json` structure.
- Mark the fixture as provisional in docs or test comments.
- Keep the parser defensive until real data is added.

Available field-evidence fixture:

```text
docs/samples/redis_lrange_251bb5c1_redacted.ndjson
```

Use this only to validate observed raw event fields and redaction cases. It does not replace the required OSProfiler report JSON fixture for the Go converter.

Report model guidance:

- Represent `info` as `map[string]any`.
- Represent `children` recursively.
- Treat top-level `info.name = total` as the source container and export it as a synthetic OTLP root span.
- Extract known fields opportunistically.
- Preserve unknown fields in `osprofiler.info_json`.
- Never assume every node has every ID or timestamp field.

## Phase 4: Python Helper

Implement `helper/osprofiler_helper.py` as a narrow adapter.

Required behavior:

- Read NDJSON requests from stdin.
- Write NDJSON responses to stdout.
- Log diagnostics to stderr only.
- Initialize OSProfiler driver from the configured connection string.
- Support `get_report`.
- Return structured errors.

Request:

```json
{"id":"1","method":"get_report","base_id":"<uuid>"}
```

Success response:

```json
{"id":"1","ok":true,"report":{}}
```

Error response:

```json
{"id":"1","ok":false,"error":{"code":"trace_not_found","message":"trace not found"}}
```

Initial error codes:

```text
bad_request
unknown_method
driver_init_failed
trace_not_found
get_report_failed
internal_error
```

## Phase 5: Go Helper Client

Implement helper management in Go:

- Start helper subprocess.
- Pass connection string safely without logging credentials.
- Send one request per line.
- Read one response per line.
- Correlate responses by `id`.
- Enforce request timeout.
- Capture stderr for bounded diagnostic logs.
- Kill helper on bridge shutdown.
- If helper exits, times out, or emits malformed protocol output, fail the current export, print a clear error, and exit the bridge process.

MVP can process one request at a time. Concurrency can wait until batch export exists.

## Phase 6: JSON-to-OTLP Conversion

Build OTLP spans from the report tree.

Rules:

- Trace ID: `base_id` UUID raw 16 bytes; fallback stable 128-bit hash.
- Synthetic root span ID: `base_id` stable 64-bit hash.
- Span ID: node `trace_id` or `id` stable 64-bit hash; fallback hash of node path.
- Parent span ID: `parent_id` stable 64-bit hash when present; otherwise tree parent span ID.
- If `parent_id == base_id`, attach the span to the synthetic root span.
- Synthetic root span has no parent span ID.
- Synthetic root span name is `osprofiler.total`.
- Span name: project-qualified operation, with HTTP/SQL summaries when available, for example `keystone.wsgi POST /v3/auth/tokens` or `keystone.db SELECT user`.
- Start/end: raw payload start/stop timestamps first; relative `started`/`finished` offsets only as fallback.
- Resource `service.name`: OSProfiler project, for example `keystone`; fallback to config value, default `osprofiler-bridge`.
- Resource `service.namespace`: `openstack`.
- Resource `service.instance.id`: OSProfiler host when known; spans are grouped by host.
- Attributes: redacted `osprofiler.info_json` plus selected OSProfiler IDs.

Use OTLP protobuf types directly instead of trying to create live OpenTelemetry SDK spans, because the converter must preserve existing trace/span IDs.

## Phase 7: OTLP HTTP Export

Export:

```text
POST <otlp.endpoint>
Content-Type: application/x-protobuf
```

Behavior:

- Respect configured timeout.
- Treat non-2xx responses as failure.
- Do not log full protobuf payload.
- Include concise error context: status code, endpoint host, trace count, span count.
- Assume the configured endpoint is OTel Collector by default. Do not add Collector-specific behavior to the exporter; it should remain generic OTLP HTTP.

MVP does not write a done marker on success. The operator can retry manually by running the same base ID again.

## Phase 8: Metrics and Health

Expose a Prometheus metrics endpoint after the first export path exists.

Recommended defaults:

```text
metrics.listen_addr = ":9090"
metrics.path = "/metrics"
```

Initial metrics:

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

Optional health endpoints:

```text
/healthz
/readyz
```

Readiness can require helper availability. Tempo reachability should be metric/log-only for MVP unless the owner decides otherwise.

## Phase 9: Tests

Minimum unit tests:

- Config env expansion and validation.
- Helper NDJSON request/response parsing.
- Helper structured error parsing.
- Report tree traversal.
- UUID to OTLP trace ID conversion.
- Stable span ID hashing.
- Parent span derivation from `parent_id` and tree parent.
- Timestamp parsing and fallbacks.
- Redaction of `db.params`.
- Redaction of sensitive key names.
- Optional redaction of `db.statement`.
- OTLP request creation from fixture JSON.

Integration-style tests:

- Helper subprocess fake returning fixture JSON.
- OTLP exporter using an HTTP test server.
- Export failure returns failure.
- Export success reports span count.

Manual verification:

- Run `osprofiler trace show <base_id> --json --connection-string <URI>` in the target environment to capture a sample.
- Run bridge against the same `base_id` with `osprofiler-tempo-bridge export --base-id <uuid> --config <path>`.
- Confirm OTel Collector receives the request and forwards it to Tempo.
- Confirm Tempo ingest metrics increased.
- Search in Grafana Tempo by trace ID first, then by `service.name = <project>` such as `keystone`.
- Confirm spans appear with stable parent-child relationships where possible.
- Confirm logs do not include Redis passwords, full connection strings, SQL params, or full trace payloads.

## MVP Success Criteria

Implementation is complete when:

1. `go test ./...` passes.
2. The fixture `docs/samples/osprofiler_trace_show_251bb5c1_redacted.json` converts into a valid OTLP protobuf request.
3. Unit tests cover config expansion, report traversal, ID conversion, timestamp conversion, redaction, and OTLP request creation.
4. Integration tests cover fake helper success/failure and fake OTLP HTTP server success/failure.
5. The Docker image builds and includes the Go bridge, Python helper, Python runtime, `osprofiler`, and Redis/Valkey Python dependency.
6. Running the container with `OSPROFILER_CONNECTION_STRING` and `OTLP_ENDPOINT` can export a real `base_id`.
7. OTel Collector receives the OTLP request and forwards it to Tempo.
8. Grafana Tempo can find the exported trace by trace ID and under `service.name = <project>` such as `keystone`.
9. Logs are checked for absence of Redis passwords, full connection strings, SQL params, and full trace payloads.

## Phase 10: Deferred Automation

After MVP works, revisit:

- `list_traces` through the helper.
- Batch export.
- Periodic polling.
- Redis-backed done marker.
- Late-event detection.
- Duplicate prevention.
- Additional OpenTelemetry semantic convention mapping beyond the current project/host resource grouping.
- HTTP/DB/RPC semantic attributes.

Do not pull these into the first implementation unless the owner explicitly expands scope.

## Agent Instructions

When implementation is approved:

- Do not change planning decisions silently. Update docs if behavior changes.
- Keep Python helper as an OSProfiler adapter, not a second bridge.
- Keep Go responsible for OTLP conversion/export and future state handling.
- Do not implement Go Redis raw event parsing for MVP.
- Do not add OpenSearch, web UI, or exactly-once delivery in MVP.
- Prefer explicit, testable behavior over hidden heuristics.
- Never log full trace payloads by default.
