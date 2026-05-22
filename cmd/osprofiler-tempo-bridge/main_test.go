package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"
)

func TestRunExportWithFakeHelperAndOTLPServer(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}

	var sawOTLP bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawOTLP = true
		var req collectortrace.ExportTraceServiceRequest
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := proto.Unmarshal(body, &req); err != nil {
			t.Fatal(err)
		}
		if len(req.ResourceSpans) != 3 {
			t.Fatalf("resource spans len = %d", len(req.ResourceSpans))
		}
		var sawComputeResource bool
		for _, rs := range req.ResourceSpans {
			if got := attr(rs.Resource.Attributes, "service.name"); got != "keystone" {
				t.Fatalf("service.name = %q, want keystone", got)
			}
			if attr(rs.Resource.Attributes, "service.instance.id") == "aolda-compute" {
				sawComputeResource = true
			}
		}
		if !sawComputeResource {
			t.Fatal("missing aolda-compute resource")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tmp := t.TempDir()
	helperPath := filepath.Join(tmp, "fake_helper.py")
	if err := os.WriteFile(helperPath, []byte(`
import json
import os
import sys

for line in sys.stdin:
    req = json.loads(line)
    with open(os.environ["REPORT_PATH"], "r", encoding="utf-8") as f:
        report = json.load(f)
    print(json.dumps({"id": req["id"], "ok": True, "report": report}), flush=True)
`), 0o600); err != nil {
		t.Fatal(err)
	}

	reportPath, err := filepath.Abs("../../docs/samples/osprofiler_trace_show_251bb5c1_redacted.json")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("REPORT_PATH", reportPath)
	t.Setenv("OSPROFILER_CONNECTION_STRING", "redis://:redacted@example:6379/0")
	t.Setenv("OTLP_ENDPOINT", server.URL)

	configPath := filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(configPath, []byte(`
osprofiler:
  connection_string: "${OSPROFILER_CONNECTION_STRING}"
helper:
  command: ["`+python+`", "`+helperPath+`"]
otlp:
  endpoint: "${OTLP_ENDPOINT}"
bridge:
  service_name: "osprofiler-bridge"
  redact_db_params: true
  redact_sensitive_keys: true
`), 0o600); err != nil {
		t.Fatal(err)
	}

	err = run([]string{
		"export",
		"--base-id", "251bb5c1-30fb-4b04-a223-4410b831d4d7",
		"--config", configPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawOTLP {
		t.Fatal("otlp server did not receive request")
	}
}

func TestRunWatchOnceExportsAndDeletes(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}

	var sawOTLP bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawOTLP = true
		var req collectortrace.ExportTraceServiceRequest
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := proto.Unmarshal(body, &req); err != nil {
			t.Fatal(err)
		}
		if len(req.ResourceSpans) == 0 {
			t.Fatal("empty resource spans")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tmp := t.TempDir()
	helperPath := filepath.Join(tmp, "fake_helper.py")
	deletePath := filepath.Join(tmp, "deleted")
	if err := os.WriteFile(helperPath, []byte(`
import json
import os
import sys

for line in sys.stdin:
    req = json.loads(line)
    method = req.get("method")
    if method == "list_traces":
        print(json.dumps({"id": req["id"], "ok": True, "traces": [{"base_id": "251bb5c1-30fb-4b04-a223-4410b831d4d7", "timestamp": "2026-04-25T15:30:00.000000"}]}), flush=True)
    elif method == "get_report":
        with open(os.environ["REPORT_PATH"], "r", encoding="utf-8") as f:
            report = json.load(f)
        print(json.dumps({"id": req["id"], "ok": True, "report": report}), flush=True)
    elif method == "delete_trace":
        with open(os.environ["DELETE_PATH"], "w", encoding="utf-8") as f:
            f.write(req.get("base_id", ""))
        print(json.dumps({"id": req["id"], "ok": True, "deleted": 1}), flush=True)
    else:
        print(json.dumps({"id": req["id"], "ok": False, "error": {"code": "unknown", "message": method}}), flush=True)
`), 0o600); err != nil {
		t.Fatal(err)
	}

	reportPath, err := filepath.Abs("../../docs/samples/osprofiler_trace_show_251bb5c1_redacted.json")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("REPORT_PATH", reportPath)
	t.Setenv("DELETE_PATH", deletePath)
	t.Setenv("OSPROFILER_CONNECTION_STRING", "redis://:redacted@example:6379/0")
	t.Setenv("OTLP_ENDPOINT", server.URL)

	statePath := filepath.Join(tmp, "state.json")
	configPath := filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(configPath, []byte(`
osprofiler:
  connection_string: "${OSPROFILER_CONNECTION_STRING}"
helper:
  command: ["`+python+`", "`+helperPath+`"]
otlp:
  endpoint: "${OTLP_ENDPOINT}"
watch:
  export_delay: "0s"
  state_file: "`+statePath+`"
  max_traces_per_poll: 10
  delete_after_export: true
`), 0o600); err != nil {
		t.Fatal(err)
	}

	err = run([]string{
		"watch",
		"--once",
		"--config", configPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawOTLP {
		t.Fatal("otlp server did not receive request")
	}
	deleted, err := os.ReadFile(deletePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(deleted) != "251bb5c1-30fb-4b04-a223-4410b831d4d7" {
		t.Fatalf("deleted base id = %q", string(deleted))
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("state file was not written: %v", err)
	}
}

func TestRunWatchOnceListsTracePageFromSavedCursor(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tmp := t.TempDir()
	helperPath := filepath.Join(tmp, "fake_helper.py")
	requestPath := filepath.Join(tmp, "list_request.json")
	if err := os.WriteFile(helperPath, []byte(`
import json
import os
import sys

for line in sys.stdin:
    req = json.loads(line)
    method = req.get("method")
    if method == "list_traces":
        with open(os.environ["REQUEST_PATH"], "w", encoding="utf-8") as f:
            json.dump(req, f)
        print(json.dumps({"id": req["id"], "ok": True, "next_cursor": "9", "traces": []}), flush=True)
    else:
        print(json.dumps({"id": req["id"], "ok": False, "error": {"code": "unexpected", "message": method}}), flush=True)
`), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("REQUEST_PATH", requestPath)
	t.Setenv("OSPROFILER_CONNECTION_STRING", "redis://:redacted@example:6379/0")
	t.Setenv("OTLP_ENDPOINT", server.URL)

	statePath := filepath.Join(tmp, "state.json")
	if err := os.WriteFile(statePath, []byte(`{"exported":{},"scan_cursor":"7"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	configPath := filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(configPath, []byte(`
osprofiler:
  connection_string: "${OSPROFILER_CONNECTION_STRING}"
helper:
  command: ["`+python+`", "`+helperPath+`"]
otlp:
  endpoint: "${OTLP_ENDPOINT}"
watch:
  export_delay: "0s"
  state_file: "`+statePath+`"
  max_traces_per_poll: 10
  scan_count: 25
  delete_after_export: true
`), 0o600); err != nil {
		t.Fatal(err)
	}

	err = run([]string{
		"watch",
		"--once",
		"--config", configPath,
	})
	if err != nil {
		t.Fatal(err)
	}

	var req map[string]any
	data, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatal(err)
	}
	if req["cursor"] != "7" {
		t.Fatalf("cursor = %#v, want 7", req["cursor"])
	}
	if req["count"] != float64(25) {
		t.Fatalf("count = %#v, want 25", req["count"])
	}

	var state map[string]any
	data, err = os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if state["scan_cursor"] != "9" {
		t.Fatalf("scan_cursor = %#v, want 9", state["scan_cursor"])
	}
}

func attr(attrs []*commonpb.KeyValue, key string) string {
	for _, kv := range attrs {
		if kv.Key == key {
			return kv.Value.GetStringValue()
		}
	}
	return ""
}
