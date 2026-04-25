# Agent Feedback Log

This document records owner instructions, corrections, and agent mistakes that future agents must check before working in this repository.

The purpose is simple: do not repeat avoidable mistakes.

## Standing Instructions

### Do Not Implement Until Planning Is Explicitly Complete

Owner instruction:

```text
기획이 끝나기 전까지는 절대 개발을 진행하지마
```

Meaning:

- Do not initialize a Go module.
- Do not scaffold source directories.
- Do not add dependencies.
- Do not write production code.
- Do not run implementation-oriented setup.
- Documentation and planning artifacts are allowed when requested.

### Record Corrections and Mistakes

Owner instruction:

```text
앞으로 내가 말하는 것에 대해서 지적하거나 너가 실수한 부분들에 대해서도 docs를 만들어줘
똑같은 실수를 다시 되풀이하지 않게
```

Meaning:

- When the owner corrects an assumption, record it here.
- When an agent makes a process or technical mistake, record it here.
- When an agent notices a risky ambiguity in the owner's PRD or plan, document it clearly instead of relying on chat history.
- Keep entries concrete and actionable.

## Current Corrections and Lessons

### 2026-04-25: Planning-Only Mode Is Binding

Context:

The owner explicitly said not to proceed with development until planning is complete.

Rule:

Future agents must treat this as a hard guardrail. If a task sounds implementation-adjacent, confirm whether it is planning-only unless the owner clearly authorizes implementation.

Allowed examples:

- PRD refinement.
- Architecture analysis.
- Source reference review.
- Open-question tracking.
- Documentation updates.

Blocked examples:

- `go mod init`
- `go get`
- Creating `cmd/` or `internal/` implementation packages.
- Writing bridge code.
- Building binaries or containers.

### 2026-04-25: Verify OSProfiler Behavior From Sources

Context:

The owner asked to inspect the referenced OSProfiler Redis/OTLP implementation before deciding trace completion behavior.

Finding:

- Redis driver writes events with `LPUSH` into `osprofiler_opt:<base_id>`.
- Redis driver reads with `LRANGE`.
- Redis backend does not write a completion marker.
- OTLP driver processes events live and does not solve Redis trace completion detection.

Rule:

Do not invent a completion marker or assume trace finality exists in Redis. This applies to future automatic polling/discovery work. For the current MVP, automatic completion detection is deferred because the operator supplies a single `base_id` explicitly.

### 2026-04-25: Done Marker Should Preserve Late-Event Detection

Context:

A simple `done = 1` marker can permanently skip late-arriving events after export.

Decision direction:

Prefer storing:

```json
{
  "exported_len": 42,
  "exported_at": "2026-04-25T12:35:45Z"
}
```

Rule:

Future automatic polling/state implementation should not blindly use a boolean done marker without revisiting late-event handling. The current single-`base_id` MVP does not write done markers.

### 2026-04-25: Approval-Driven Feature Workflow

Context:

The owner defined the expected loop for all future work:

```text
1. Discuss theoretical implementation shape and tradeoffs.
2. Implement only after owner approval.
3. Run QA to verify behavior.
4. Present implementation and QA results clearly.
```

Correction:

Agents should not jump directly from a feature idea to code, even after planning mode ends. The owner wants to evaluate tradeoffs before implementation.

Rule:

For meaningful features, first present options, tradeoffs, and a recommendation. Wait for explicit approval. Then implement, run QA, and summarize results in owner-friendly language.

### 2026-04-26: Do Not Reimplement OSProfiler Redis Parsing in Go for MVP

Context:

The owner approved a new direction for the bridge:

```text
OSProfiler -> Redis -> Python helper using OSProfiler driver -> Go JSON-to-OTLP exporter -> Tempo
```

Correction:

The MVP should not be planned as a Go service that directly scans Redis, reads `osprofiler_opt:*` lists, and matches raw `*-start` / `*-stop` events. OSProfiler already has driver/report-generation code for this.

Rule:

For MVP, use OSProfiler report JSON as the intermediate format. The Python helper should be a narrow OSProfiler adapter, and Go should own helper process control, JSON-to-OTLP conversion, OTLP export, metrics, and future state handling.

### 2026-04-26: Approved MVP Interface Decisions

Context:

The owner approved these initial implementation-planning decisions:

```text
single base_id export
stdin/stdout NDJSON
OTel Collector OTLP HTTP endpoint by default
actual OSProfiler JSON sample before final struct lock-in
```

Rule:

Future planning should treat these as the baseline unless the owner explicitly changes them. Automatic polling, Redis done markers, late-event handling, and batch export are deferred.

### 2026-04-26: Sanitized Redis LRANGE Sample Captured

Context:

The owner provided a real Redis `LRANGE osprofiler_opt:<base_id> 0 -1` output for base ID `251bb5c1-30fb-4b04-a223-4410b831d4d7`.

Correction:

The sample is Redis raw event output, not OSProfiler `trace show --json` report output. It is useful for field evidence and redaction tests, but it does not replace the required report JSON sample for the MVP converter.

Rule:

Store only sanitized samples. Do not persist Redis passwords, inline credential-bearing CLI commands, raw SQL parameter values, or other secrets. The sanitized sample is stored at `docs/samples/redis_lrange_251bb5c1_redacted.ndjson`.

### 2026-04-26: OSProfiler Report JSON Sample Captured

Context:

The owner provided a real `osprofiler trace show --json` output for base ID `251bb5c1-30fb-4b04-a223-4410b831d4d7`.

Finding:

- The top-level `total` node is a report container with no `trace_id`.
- Real spans live under recursive `children`.
- Span nodes carry top-level `trace_id` and `parent_id`.
- `info.started` and `info.finished` are relative millisecond offsets, not Unix timestamps.
- Exact absolute timestamps are available in nested raw payload keys such as `meta.raw_payload.wsgi-start.timestamp`.

Rule:

Use `docs/samples/osprofiler_trace_show_251bb5c1_redacted.json` as the first report-model fixture. Do not store Redis connection strings with passwords or raw SQL parameter values.

### 2026-04-26: Container and Collector Deployment Decisions

Context:

The owner decided the bridge will run as a Docker container and send OTLP to an OTel Collector. Configuration and secrets should be passed through config/environment variables.

Rule:

Plan the MVP image as a single container containing the Go bridge binary, Python helper, Python runtime, `osprofiler`, and Redis/Valkey Python dependencies. Default OTLP endpoint should point at OTel Collector. Do not bake connection strings, Redis passwords, or endpoint-specific secrets into the image.

### 2026-04-26: Helper Failure Policy

Context:

The owner approved the MVP helper failure behavior.

Rule:

If the Python helper exits, times out, or emits malformed protocol output, fail the current export, print a clear error, and exit the bridge process. Do not add automatic helper restart/retry in the single-`base_id` MVP.

### 2026-04-26: CLI and MVP Success Criteria

Context:

The owner approved the recommended CLI shape and asked to record MVP success criteria.

Rule:

Use `osprofiler-tempo-bridge export --base-id <uuid> --config <path>` for MVP. MVP completion requires fixture-to-OTLP conversion, unit/integration tests, Docker image build, real container export through OTel Collector to Tempo, Grafana lookup by `service.name=osprofiler-bridge`, and log checks proving secrets/full trace payloads are not emitted.

## Entry Template

Use this format for future updates:

```md
### YYYY-MM-DD: Short Title

Context:

What happened, what the owner said, or what the agent got wrong.

Correction:

The corrected understanding.

Rule:

Concrete behavior future agents should follow.
```
