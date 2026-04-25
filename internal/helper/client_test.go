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
