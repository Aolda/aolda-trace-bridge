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

func TestClientListTracePageSendsCursorAndCount(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-helper.py")
	if err := os.WriteFile(script, []byte(`#!/usr/bin/env python3
import json
import sys

for line in sys.stdin:
    req = json.loads(line)
    if req.get("cursor") != "7" or req.get("count") != 25:
        print(json.dumps({"id": "1", "ok": False, "error": {"code": "bad_request", "message": json.dumps(req)}}), flush=True)
        continue
    print(json.dumps({"id": "1", "ok": True, "next_cursor": "9", "traces": [{"base_id": "base-1"}]}), flush=True)
`), 0o700); err != nil {
		t.Fatal(err)
	}

	client := NewClient([]string{script}, "redis://redacted", time.Second)
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	page, err := client.ListTracePage(context.Background(), "7", 25)
	if err != nil {
		t.Fatal(err)
	}
	if page.NextCursor != "9" {
		t.Fatalf("next cursor = %q, want 9", page.NextCursor)
	}
	if len(page.Traces) != 1 || page.Traces[0].BaseID != "base-1" {
		t.Fatalf("unexpected traces: %+v", page.Traces)
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

func TestClientRestartsHelperAfterTimeout(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-helper.py")
	countPath := filepath.Join(t.TempDir(), "count")
	if err := os.WriteFile(script, []byte(`#!/usr/bin/env python3
import json
import os
import sys
import time

count_path = os.environ["COUNT_PATH"]
try:
    with open(count_path, "r", encoding="utf-8") as f:
        count = int(f.read())
except Exception:
    count = 0
count += 1
with open(count_path, "w", encoding="utf-8") as f:
    f.write(str(count))

for line in sys.stdin:
    req = json.loads(line)
    if count == 1:
        time.sleep(2)
    print(json.dumps({"id": req["id"], "ok": True, "traces": [{"base_id": "base-1"}]}), flush=True)
`), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COUNT_PATH", countPath)

	client := NewClient([]string{script}, "redis://redacted", time.Second)
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if _, err := client.ListTraces(context.Background()); err == nil {
		t.Fatal("expected first helper request to time out")
	}

	traces, err := client.ListTraces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(traces) != 1 || traces[0].BaseID != "base-1" {
		t.Fatalf("unexpected traces after restart: %+v", traces)
	}
}

func TestClientRetriesRequestAfterHelperExitsBeforeResponse(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-helper.py")
	countPath := filepath.Join(t.TempDir(), "count")
	if err := os.WriteFile(script, []byte(`#!/usr/bin/env python3
import json
import os
import sys

count_path = os.environ["COUNT_PATH"]
try:
    with open(count_path, "r", encoding="utf-8") as f:
        count = int(f.read())
except Exception:
    count = 0
count += 1
with open(count_path, "w", encoding="utf-8") as f:
    f.write(str(count))

if count == 1:
    sys.exit(0)

for line in sys.stdin:
    req = json.loads(line)
    print(json.dumps({"id": req["id"], "ok": True, "traces": [{"base_id": "base-1"}]}), flush=True)
`), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COUNT_PATH", countPath)

	client := NewClient([]string{script}, "redis://redacted", time.Second)
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	traces, err := client.ListTraces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(traces) != 1 || traces[0].BaseID != "base-1" {
		t.Fatalf("unexpected traces after retry: %+v", traces)
	}
}
