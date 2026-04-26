#!/usr/bin/env python3
import json
import os
import re
import sys
import traceback


_ENGINE = None


def _response_ok(request_id, report):
    return {"id": request_id, "ok": True, "report": report}


def _response_error(request_id, code, message):
    return {"id": request_id, "ok": False, "error": {"code": code, "message": message}}


def _write(message):
    sys.stdout.write(json.dumps(message, separators=(",", ":"), default=str))
    sys.stdout.write("\n")
    sys.stdout.flush()


def _redact_message(message):
    text = str(message)
    connection_string = os.environ.get("OSPROFILER_CONNECTION_STRING", "")
    if connection_string:
        text = text.replace(connection_string, "<redacted connection string>")
    text = re.sub(r"redis://:[^@\\s]+@", "redis://:<redacted>@", text)
    text = re.sub(r"rediss://:[^@\\s]+@", "rediss://:<redacted>@", text)
    return text


def _print_exception(prefix):
    print(prefix, file=sys.stderr)
    print(_redact_message(traceback.format_exc()), file=sys.stderr)


def _get_engine():
    global _ENGINE
    if _ENGINE is not None:
        return _ENGINE

    connection_string = os.environ.get("OSPROFILER_CONNECTION_STRING", "")
    if not connection_string:
        raise RuntimeError("OSPROFILER_CONNECTION_STRING is required")

    from osprofiler.drivers import base

    # OSProfiler CLI initializes oslo.config before calling the driver. This
    # helper only reads reports, so pass a plain config object and avoid
    # requiring OpenStack service config groups inside the bridge container.
    _ENGINE = base.get_driver(connection_string, conf={})
    return _ENGINE


def _handle(request):
    request_id = str(request.get("id", ""))
    method = request.get("method")

    if not request_id:
        return _response_error("", "bad_request", "id is required")
    if method != "get_report":
        return _response_error(request_id, "unknown_method", "unknown method")

    base_id = request.get("base_id")
    if not base_id:
        return _response_error(request_id, "bad_request", "base_id is required")

    try:
        engine = _get_engine()
    except Exception as exc:
        _print_exception("driver initialization failed")
        return _response_error(request_id, "driver_init_failed", _redact_message(exc))

    try:
        report = engine.get_report(base_id)
    except Exception as exc:
        _print_exception("get_report failed")
        return _response_error(request_id, "get_report_failed", _redact_message(exc))

    if report is None:
        return _response_error(request_id, "trace_not_found", "trace not found")

    return _response_ok(request_id, report)


def main():
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            request = json.loads(line)
        except Exception as exc:
            _write(_response_error("", "bad_request", f"invalid json: {exc}"))
            continue

        try:
            _write(_handle(request))
        except Exception as exc:
            _print_exception("internal helper error")
            request_id = str(request.get("id", ""))
            _write(_response_error(request_id, "internal_error", _redact_message(exc)))


if __name__ == "__main__":
    main()
