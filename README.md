# OSProfiler Redis-to-Tempo Bridge

Planning repository for a bridge that reuses OSProfiler's Python driver/report generation, converts OSProfiler report JSON to OTLP trace protobuf in Go, and sends it to an OTel Collector over OTLP HTTP for forwarding to Tempo.

## Current Status

MVP implementation and watch-mode automation are present.

Implemented scope:

- Single `base_id` export.
- Watch mode that polls OSProfiler trace IDs in bounded batches.
- Successful watch exports can delete exported OSProfiler Redis keys.
- Local state file to avoid duplicate export attempts across polls/restarts.
- Python helper using OSProfiler driver/report generation.
- Go report JSON to OTLP conversion.
- OTLP HTTP export to OTel Collector.
- Docker image packaging.
- GitHub Actions CI and GHCR publishing workflow.

Deferred scope:

- Late-event handling.
- Multi-worker Redis locks.
- HAProxy/API gateway automatic `X-Trace-*` header injection.

## Document Index

- [Product Requirements](docs/PRD.md): normalized PRD, MVP scope, exclusions, security, and verification.
- [Architecture Notes](docs/ARCHITECTURE.md): Python helper + Go exporter architecture, helper protocol, OTLP mapping, and deferred automation.
- [Implementation Plan](docs/IMPLEMENTATION_PLAN.md): phased implementation plan for future agents after planning is approved.
- [Open Questions](docs/OPEN_QUESTIONS.md): decisions still needing owner approval.
- [Working Workflow](docs/WORKFLOW.md): required discussion, approval, implementation, QA, and reporting loop.
- [Agent Feedback Log](docs/AGENT_FEEDBACK_LOG.md): owner instructions, corrections, and agent mistakes to avoid repeating.
- [Samples](docs/samples/README.md): sanitized field samples from the target environment.

## Primary References

- OSProfiler Redis driver: https://docs.openstack.org/osprofiler/latest/_modules/osprofiler/drivers/redis_driver.html
- OSProfiler OTLP driver: https://docs.openstack.org/osprofiler/latest/_modules/osprofiler/drivers/otlp.html
- OSProfiler API/event model: https://docs.openstack.org/osprofiler/latest/user/api.html

## Container Publishing

GitHub Actions builds and publishes the Docker image to GHCR on pushes to `main`, version tags, and manual workflow runs.

Image name:

```text
ghcr.io/<owner>/<repo>
```

Tags include:

```text
latest
main
sha-<commit>
vX.Y.Z
```

Pull example:

```sh
docker pull ghcr.io/<owner>/<repo>:latest
```

## Usage

Export one trace:

```sh
docker run --rm --network host \
  -e OSPROFILER_CONNECTION_STRING \
  -e OTLP_ENDPOINT \
  ghcr.io/aolda/aolda-trace-bridge:latest \
  export \
  --base-id "$BASE_ID" \
  --config /etc/osprofiler-tempo-bridge/config.yaml
```

Run continuous polling:

```sh
docker run --rm --network host \
  -e OSPROFILER_CONNECTION_STRING \
  -e OTLP_ENDPOINT \
  -v osprofiler-tempo-bridge-state:/var/lib/osprofiler-tempo-bridge \
  ghcr.io/aolda/aolda-trace-bridge:latest \
  watch \
  --config /etc/osprofiler-tempo-bridge/config.yaml
```

Watch mode defaults:

```text
poll_interval: 30s
export_delay: 2m
max_traces_per_poll: 100
delete_after_export: true
state_file: /var/lib/osprofiler-tempo-bridge/state.json
```

`export_delay` avoids exporting and deleting traces that may still be receiving late OSProfiler events. `max_traces_per_poll` keeps each polling cycle bounded so Redis, the helper, and the OTLP endpoint are not flooded. The state file records exported `base_id`s and deletion completion only; full OSProfiler report JSON is not written to disk.
