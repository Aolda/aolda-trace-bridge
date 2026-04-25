# Samples

This directory contains sanitized samples captured from the target OpenStack/Kolla environment.

## `redis_lrange_251bb5c1_redacted.ndjson`

Source shape:

```text
docker exec redis redis-cli ... LRANGE osprofiler_opt:<base_id> 0 -1
```

Important:

- This is Redis raw event output, not `osprofiler trace show --json` report output.
- The Redis password from the capture command is intentionally not stored.
- SQL parameter values are redacted.
- The sample is useful for validating source field names and redaction behavior.
- Go MVP code should still consume OSProfiler report JSON returned by the Python helper, not this raw Redis event format.

## `osprofiler_trace_show_251bb5c1_redacted.json`

Source shape:

```text
docker exec keystone /var/lib/kolla/venv/bin/python -m osprofiler.cmd.shell trace show --json <base_id> --connection-string <redis-uri>
```

Important:

- This is the MVP-relevant OSProfiler report JSON shape.
- The Redis connection string and password are intentionally not stored.
- SQL statements and SQL parameter values are redacted.
- The sample preserves the observed report tree shape, relative timing fields, raw payload timestamp fields, `stats`, and parent-child IDs.
- It is representative, not complete: only a subset of repeated DB children is retained to keep the fixture compact, and `stats.db` is adjusted to match the retained children.
