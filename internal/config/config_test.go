package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadFileExpandsEnvAndDefaults(t *testing.T) {
	t.Setenv("OSPROFILER_CONNECTION_STRING", "redis://:secret@example:6379/0")
	t.Setenv("OTLP_ENDPOINT", "http://otel-collector:4318/v1/traces")

	path := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(path, []byte(`
osprofiler:
  connection_string: "${OSPROFILER_CONNECTION_STRING}"
helper:
  command: ["python3", "helper.py"]
otlp:
  endpoint: "${OTLP_ENDPOINT}"
`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.OSProfiler.ConnectionString != "redis://:secret@example:6379/0" {
		t.Fatalf("unexpected connection string: %q", cfg.OSProfiler.ConnectionString)
	}
	if cfg.OTLP.Endpoint != "http://otel-collector:4318/v1/traces" {
		t.Fatalf("unexpected endpoint: %q", cfg.OTLP.Endpoint)
	}
	if cfg.Bridge.ServiceName != "osprofiler-bridge" {
		t.Fatalf("unexpected service name: %q", cfg.Bridge.ServiceName)
	}
	if !cfg.Bridge.RedactDBParams {
		t.Fatal("redact_db_params should default true")
	}
	if !cfg.Bridge.RedactSensitiveKeys {
		t.Fatal("redact_sensitive_keys should default true")
	}
	if cfg.Helper.RequestTimeout != 10*time.Second {
		t.Fatalf("unexpected request timeout: %s", cfg.Helper.RequestTimeout)
	}
	if cfg.Watch.PollInterval != 30*time.Second {
		t.Fatalf("unexpected watch poll interval: %s", cfg.Watch.PollInterval)
	}
	if cfg.Watch.ExportDelay != 2*time.Minute {
		t.Fatalf("unexpected watch export delay: %s", cfg.Watch.ExportDelay)
	}
	if cfg.Watch.StateFile != "/var/lib/osprofiler-tempo-bridge/state.json" {
		t.Fatalf("unexpected watch state file: %q", cfg.Watch.StateFile)
	}
	if cfg.Watch.MaxTracesPerPoll != 100 {
		t.Fatalf("unexpected max traces per poll: %d", cfg.Watch.MaxTracesPerPoll)
	}
	if !cfg.Watch.DeleteAfterExport {
		t.Fatal("delete_after_export should default true")
	}
}

func TestLoadFileWatchOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(path, []byte(`
osprofiler:
  connection_string: "redis://example"
helper:
  command: ["python3", "helper.py"]
otlp:
  endpoint: "http://otel-collector:4318/v1/traces"
watch:
  poll_interval: "5s"
  export_delay: "2m"
  state_file: "/tmp/state.json"
  max_traces_per_poll: 7
  delete_after_export: false
`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Watch.PollInterval != 5*time.Second {
		t.Fatalf("poll interval = %s", cfg.Watch.PollInterval)
	}
	if cfg.Watch.ExportDelay != 2*time.Minute {
		t.Fatalf("export delay = %s", cfg.Watch.ExportDelay)
	}
	if cfg.Watch.StateFile != "/tmp/state.json" {
		t.Fatalf("state file = %q", cfg.Watch.StateFile)
	}
	if cfg.Watch.MaxTracesPerPoll != 7 {
		t.Fatalf("max traces per poll = %d", cfg.Watch.MaxTracesPerPoll)
	}
	if cfg.Watch.DeleteAfterExport {
		t.Fatal("delete_after_export should be false")
	}
}

func TestLoadFileRequiresEndpoint(t *testing.T) {
	t.Setenv("OSPROFILER_CONNECTION_STRING", "redis://example")

	path := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(path, []byte(`
osprofiler:
  connection_string: "${OSPROFILER_CONNECTION_STRING}"
helper:
  command: ["python3", "helper.py"]
`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	_, err = LoadFile(path)
	if err == nil {
		t.Fatal("expected validation error")
	}
}
