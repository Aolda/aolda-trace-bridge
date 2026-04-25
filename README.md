# OSProfiler Redis-to-Tempo Bridge

Planning repository for a bridge that reuses OSProfiler's Python driver/report generation, converts OSProfiler report JSON to OTLP trace protobuf in Go, and sends it to an OTel Collector over OTLP HTTP for forwarding to Tempo.

## Current Status

MVP implementation is present.

Implemented MVP scope:

- Single `base_id` export.
- Python helper using OSProfiler driver/report generation.
- Go report JSON to OTLP conversion.
- OTLP HTTP export to OTel Collector.
- Docker image packaging.
- GitHub Actions CI and GHCR publishing workflow.

Deferred scope:

- Automatic trace discovery.
- Polling.
- Batch export.
- Redis done markers.
- Late-event handling.

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
