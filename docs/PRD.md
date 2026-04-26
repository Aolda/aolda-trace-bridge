# PRD: OSProfiler Report JSON-to-OTLP Bridge

## Goal

Build a bridge for OpenStack Kolla-Ansible environments that reuses OSProfiler's own storage driver/report generation logic, converts OSProfiler report JSON into OTLP trace protobuf, and exports it to an OTel Collector over OTLP HTTP for forwarding to Tempo.

Target MVP flow:

```text
OpenStack services
  -> OSProfiler
  -> Redis/Valkey backend
  -> Python helper using OSProfiler driver
  -> Go JSON-to-OTLP exporter
  -> OTel Collector OTLP HTTP endpoint
  -> Tempo
  -> Grafana
```

The bridge should not reimplement OSProfiler Redis raw event parsing in Go.

## Background

Kolla-Ansible can configure OSProfiler with Redis, Valkey, or OpenSearch backends. The OSProfiler OTLP backend requires OpenTelemetry Python packages inside OpenStack service containers, which creates image customization and operational burden.

This project avoids service image customization by keeping OSProfiler on the Redis/Valkey backend and running a separate bridge process outside the OpenStack service containers.

OSProfiler already knows how to read Redis-backed trace events and turn them into a report tree through its storage driver. The bridge should use that report JSON as the intermediate format instead of reading Redis lists and pairing start/stop events itself.

Sanitized samples from the target environment are stored under `docs/samples/`, including a real `osprofiler trace show --json` report fixture that should be used as the first converter baseline.

## Kolla-Ansible Configuration

Redis-based deployment:

```yaml
enable_osprofiler: "yes"
enable_redis: "yes"
osprofiler_backend: "redis"
```

Valkey-based deployment:

```yaml
enable_osprofiler: "yes"
enable_valkey: "yes"
osprofiler_backend: "valkey"
```

Expected service configuration after applying Kolla-Ansible:

```ini
[profiler]
enabled = true
trace_sqlalchemy = true
hmac_keys = <osprofiler_secret>
connection_string = redis://...
```

## Approved MVP Scope

Approved on 2026-04-26:

```text
single base_id export
stdin/stdout NDJSON helper protocol
default export target is an OTel Collector OTLP HTTP endpoint
Tempo direct remains possible by config override
actual OSProfiler JSON sample must be obtained before finalizing Go structs
```

MVP input:

```text
base_id / trace_id supplied explicitly by the operator
```

MVP CLI:

```text
osprofiler-tempo-bridge export --base-id <uuid> --config <path>
```

MVP output:

```text
one OTLP TraceServiceRequest exported to the configured endpoint
```

## Functional Requirements

1. Accept a single `base_id`/trace ID as input.
2. Load config for OSProfiler connection string, Python helper command, export endpoint, timeout, and redaction behavior.
3. Start a long-running Python helper subprocess.
4. Communicate with the helper over stdin/stdout NDJSON.
5. Request `get_report(base_id)` from the helper.
6. The helper must use OSProfiler's Python driver API to load the storage driver and call `engine.get_report(base_id)`.
7. The helper must return either a report JSON object or a structured error response.
8. Go must validate the report shape enough to avoid panics and bad OTLP output.
9. Go must convert the report tree into OTLP spans using the approved MVP mapping.
10. Go must send OTLP protobuf to the configured HTTP endpoint. The default target is OTel Collector.
11. Non-2xx responses and timeouts must be treated as export failures.
12. The bridge must not log full trace payloads by default.
13. If the Python helper exits, times out, or emits malformed protocol output, the bridge must fail the current export with a clear error and exit.

## Explicit MVP Non-Goals

- Do not scan Redis directly for `osprofiler_opt:*`.
- Do not parse Redis raw list entries in Go.
- Do not implement start/stop pair matching in Go.
- Do not implement automatic polling.
- Do not implement batch export.
- Do not implement done markers or late-event detection.
- Do not delete OSProfiler Redis keys.
- Do not implement complete OpenTelemetry semantic convention mapping.
- Do not add OpenSearch support.
- Do not modify OpenStack service container images.

## Python Helper Contract

The Python helper is an OSProfiler adapter only.

Responsibilities:

- Initialize OSProfiler storage driver from the configured connection string.
- Serve `get_report` requests.
- Return OSProfiler report JSON exactly enough for Go to convert it.
- Return structured errors for not found, driver initialization failure, malformed request, and unexpected exceptions.

Non-responsibilities:

- No OTLP export.
- No retry policy.
- No Tempo awareness.
- No Redis lock or done-marker state.
- No polling loop.

Example request:

```json
{"id":"1","method":"get_report","base_id":"<uuid>"}
```

Example success response:

```json
{"id":"1","ok":true,"report":{}}
```

Example error response:

```json
{"id":"1","ok":false,"error":{"code":"trace_not_found","message":"trace not found"}}
```

## Go Bridge Contract

The Go bridge owns the operational control plane and OTLP export.

Responsibilities:

- Config loading and validation.
- Python helper subprocess lifecycle.
- Request/response correlation and timeout handling.
- OSProfiler report JSON validation.
- Redaction before logs and OTLP attributes.
- OTLP span generation.
- OTLP HTTP export to Tempo or OTel Collector.
- Metrics and health endpoints after the first working export path exists.

## Deployment Model

The MVP bridge runs as a Docker container built from this project.

The container image should include:

- Go bridge binary.
- Python helper.
- Python runtime.
- `osprofiler` Python package.
- Redis/Valkey Python dependency required by OSProfiler.

Runtime configuration should be passed through config files and environment variables. Secrets must not be baked into the image.

Container execution shape:

```text
docker run --rm \
  -e OSPROFILER_CONNECTION_STRING=... \
  -e OTLP_ENDPOINT=http://otel-collector:4318/v1/traces \
  -v ./config.yaml:/etc/osprofiler-tempo-bridge/config.yaml:ro \
  osprofiler-tempo-bridge:<tag> \
  export --base-id <uuid> --config /etc/osprofiler-tempo-bridge/config.yaml
```

## MVP OTLP Mapping

The MVP mapping is intentionally simple.

Trace ID:

```text
base_id UUID -> OTLP trace_id raw 16 bytes
non-UUID base_id -> stable 128-bit hash
```

Span ID:

```text
OSProfiler node trace_id/id -> stable 64-bit hash
fallback -> stable 64-bit hash of the node path
```

Parent span ID:

```text
parent_id if present and not base_id -> stable 64-bit hash
parent_id == base_id -> synthetic root span id
otherwise derive from the report tree parent
synthetic root span -> empty parent_span_id
```

Span name:

```text
<project>.<operation>
wsgi -> <project>.wsgi <METHOD> <PATH> when request metadata exists
db -> <project>.db <SQL_OPERATION> <TABLE> when SQL metadata exists
fallback -> operation or "osprofiler.span"
```

Timestamps:

```text
raw payload start/stop timestamps if present
else trace start plus started/finished relative millisecond offsets
else timestamp for both start and end
```

Resource attributes:

```text
service.name = <project>, for example "keystone"
service.namespace = "openstack"
service.instance.id = <host> when known
host.name = <host> when known
fallback service.name = bridge.service_name
```

Span attributes:

```text
osprofiler.info_json = redacted JSON string of node info
osprofiler.base_id
osprofiler.trace_id
osprofiler.parent_id
```

Later versions can promote selected fields into semantic attributes such as HTTP, DB, RPC, host, project, and service.

## Security Requirements

- Redact `db.params` by default.
- Redact keys whose names indicate credentials, tokens, secrets, passwords, auth headers, or cookies.
- Support optional redaction for `db.statement`.
- Inject Redis passwords and Tempo credentials through environment variables or config.
- Inject the OSProfiler connection string through an environment variable such as `OSPROFILER_CONNECTION_STRING`.
- Inject the OTLP endpoint through config or an environment variable such as `OTLP_ENDPOINT`.
- Do not log full trace payloads by default.
- Avoid logging raw SQL parameters, Tempo credentials, Redis credentials, or full HTTP headers.
- Never commit Redis CLI commands containing `-a`, `--pass`, full connection strings with passwords, or equivalent inline secrets.

## Operating Model

MVP delivery model:

```text
operator-triggered single trace export
```

MVP retry model:

```text
manual retry by rerunning the same base_id export
```

Longer-term delivery model:

```text
at-least-once
```

Automatic discovery, duplicate prevention, done markers, and late-event handling are deferred until after the single-trace path is proven.

## MVP Success Criteria

The MVP is successful when:

1. The sanitized OSProfiler report fixture converts to an OTLP protobuf request.
2. Unit tests cover config expansion, report traversal, ID conversion, timestamp conversion, redaction, and OTLP request creation.
3. Integration tests cover fake helper success/failure and fake OTLP HTTP server success/failure.
4. The Docker image builds with the Go bridge, Python helper, Python runtime, `osprofiler`, and Redis/Valkey Python dependency.
5. A container run exports a real `base_id` using config/env-provided `OSPROFILER_CONNECTION_STRING` and `OTLP_ENDPOINT`.
6. OTel Collector receives the request and forwards it to Tempo.
7. Grafana Tempo can find the exported trace by trace ID and under `service.name = <project>` such as `keystone`.
8. Logs do not contain Redis passwords, full connection strings, SQL params, or full trace payloads.
