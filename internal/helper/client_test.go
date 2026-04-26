package helper

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClientGetReport(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-helper.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
while IFS= read -r line; do
  printf '%s\n' '{"id":"1","ok":true,"report":{"info":{"name":"total"},"children":[]}}'
done
`), 0o700); err != nil {
		t.Fatal(err)
	}

	client := NewClient([]string{script}, "redis://redacted", time.Second)
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	report, err := client.GetReport(context.Background(), "base-id")
	if err != nil {
		t.Fatal(err)
	}
	if len(report) == 0 {
		t.Fatal("empty report")
	}
}

func TestClientListTraces(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-helper.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
while IFS= read -r line; do
  printf '%s\n' '{"id":"1","ok":true,"traces":[{"base_id":"base-1","timestamp":"2026-04-27T00:00:00"}]}'
done
`), 0o700); err != nil {
		t.Fatal(err)
	}

	client := NewClient([]string{script}, "redis://redacted", time.Second)
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	traces, err := client.ListTraces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(traces) != 1 {
		t.Fatalf("trace count = %d, want 1", len(traces))
	}
	if traces[0].BaseID != "base-1" {
		t.Fatalf("base id = %q", traces[0].BaseID)
	}
}

func TestClientDeleteTrace(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-helper.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
while IFS= read -r line; do
  printf '%s\n' '{"id":"1","ok":true,"deleted":2}'
done
`), 0o700); err != nil {
		t.Fatal(err)
	}

	client := NewClient([]string{script}, "redis://redacted", time.Second)
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	deleted, err := client.DeleteTrace(context.Background(), "base-id")
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("deleted = %d, want 2", deleted)
	}
}

func TestClientMalformedResponseFails(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-helper.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
while IFS= read -r line; do
  printf '%s\n' 'not-json'
done
`), 0o700); err != nil {
		t.Fatal(err)
	}

	client := NewClient([]string{script}, "redis://redacted", time.Second)
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if _, err := client.GetReport(context.Background(), "base-id"); err == nil {
		t.Fatal("expected malformed response error")
	}
}
