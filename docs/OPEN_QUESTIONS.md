# Open Questions

These decisions should be closed before implementation starts. Items marked "Decided" reflect owner-approved planning as of 2026-04-26.

## Decided: MVP Shape

Decision:

```text
single base_id export
Go bridge + long-running Python helper
stdin/stdout NDJSON
default OTLP HTTP endpoint points to OTel Collector
actual OSProfiler JSON sample before final Go struct lock-in
```

Reason:

This avoids reimplementing OSProfiler Redis raw parsing in Go while avoiding the per-trace process cost of repeatedly invoking `osprofiler trace show --json`.

## Decided: Intermediate Format

Decision:

Use OSProfiler report JSON as the bridge's intermediate format.

Reason:

OSProfiler already owns Redis event decoding and report tree construction. The bridge should convert that report JSON into OTLP rather than duplicate storage-driver behavior.

## Decided: Helper Protocol

Decision:

Use stdin/stdout NDJSON between Go and the Python helper.

Reason:

It keeps the helper private to the Go process and avoids adding a second service port, auth surface, or deployment unit for MVP.

## Decided: Export Endpoint

Decision:

Use OTLP HTTP protobuf and send to OTel Collector by default. Tempo direct remains possible by changing config, but the planned deployment path is bridge -> OTel Collector -> Tempo.

Default:

```text
http://<otel-collector>:4318/v1/traces
```

## Decided: Deployment Packaging

Decision:

Build the Go bridge as a Docker image and run it as a container.

The image should include:

- Go bridge binary.
- Python helper script.
- Python runtime.
- `osprofiler` Python package.
- Redis/Valkey Python dependency required by OSProfiler.

Configuration and secrets should be passed through config and environment variables, not baked into the image.

## Decided: Resource Service Name

Decision:

```text
service.name = <project>, for example "keystone"
service.namespace = "openstack"
service.instance.id = <host> when known
fallback service.name = "osprofiler-bridge"
```

Reason:

The MVP proved end-to-end export. The next mapping step groups spans by OSProfiler project and host so Grafana shows OpenStack services and instances instead of only the bridge process.

## Decided: MVP ID Conversion

Decision:

```text
base_id UUID -> OTLP trace_id raw 16 bytes
non-UUID base_id -> stable 128-bit hash
node trace_id/id -> stable 64-bit hash for span_id
parent_id -> stable 64-bit hash for parent_span_id
fallback parent -> report tree parent span_id
```

Reason:

OTLP trace IDs are 16 bytes and span IDs are 8 bytes. UUID `base_id` can be represented exactly as a trace ID; span IDs need deterministic narrowing.

## Decided: MVP Attribute Strategy

Decision:

Store redacted node `info` as:

```text
osprofiler.info_json
```

Also include selected IDs as OSProfiler-specific attributes where available.

Reason:

This preserves useful source data without committing to full semantic convention mapping in the first version.

## Decided: MVP Redaction Defaults

Decision:

```text
redact_db_params = true
redact_sensitive_keys = true
redact_db_statement = false
```

Reason:

SQL params and credential-like keys are high-risk. SQL statements can be useful for PoC debugging, but should remain configurable.

## Decided: Real JSON Sample

One actual `osprofiler trace show --json` sample from the target environment has been captured and sanitized.

Path:

```text
docs/samples/osprofiler_trace_show_251bb5c1_redacted.json
```

A sanitized Redis raw `LRANGE` sample is available at:

```text
docs/samples/redis_lrange_251bb5c1_redacted.ndjson
```

Report sample findings:

```text
top-level total node is a container
real spans are recursive children
span nodes carry trace_id and parent_id
info.started / info.finished are relative millisecond offsets
raw payload start/stop timestamps are available under info["meta.raw_payload.*"]
stats includes kind counts and durations
```

Implementation may use this fixture as the first report model baseline.

## Decided: Helper Failure Policy

Decision:

If the Python helper exits, times out, or emits malformed protocol output, fail the current export, print a clear error, and exit the bridge process.

Reason:

The MVP is a single `base_id` export command. Restart/retry policy can be added after the happy path is proven and batch or polling mode exists.

## Decided: CLI UX

Decision:

Use an explicit `export` subcommand:

```text
osprofiler-tempo-bridge export --base-id <uuid> --config <path>
```

Reason:

The MVP is a single-trace export command, and an explicit subcommand leaves room for future `serve`, `batch`, or `list` commands.

## Decided: MVP Success Criteria

The MVP is successful when all of these are true:

1. The sanitized report fixture can be converted into an OTLP protobuf request.
2. Unit tests pass for config expansion, report traversal, ID conversion, timestamp conversion, redaction, and OTLP request creation.
3. Integration tests pass with a fake helper and fake OTLP HTTP server.
4. The Docker image builds and contains the Go bridge, Python helper, Python runtime, `osprofiler`, and Redis/Valkey Python dependency.
5. A container run can export a real `base_id` using `OSPROFILER_CONNECTION_STRING` and `OTLP_ENDPOINT`.
6. OTel Collector receives the request and forwards it to Tempo.
7. Grafana Tempo can find the trace by trace ID and under `service.name = <project>` such as `keystone`.
8. Logs do not contain Redis passwords, full connection strings, SQL params, or full trace payloads.

## Deferred: Automatic Discovery and Done State

These are not part of the first implementation:

- `list_traces`
- Redis scanning
- polling
- done markers
- late-event detection
- duplicate prevention
- delete exported Redis keys

Revisit after single-trace export works end to end.
