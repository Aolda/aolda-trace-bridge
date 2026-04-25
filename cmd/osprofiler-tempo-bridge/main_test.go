package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
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
		if len(req.ResourceSpans) != 1 {
			t.Fatalf("resource spans len = %d", len(req.ResourceSpans))
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
