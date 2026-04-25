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
