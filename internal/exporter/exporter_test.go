package exporter

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

func TestExporterPostsProtobuf(t *testing.T) {
	var sawRequest bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRequest = true
		if r.Header.Get("Content-Type") != "application/x-protobuf" {
			t.Fatalf("content-type = %q", r.Header.Get("Content-Type"))
		}
		var req collectortrace.ExportTraceServiceRequest
		if err := proto.Unmarshal(mustReadBody(t, r), &req); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	exp := New(server.URL, time.Second)
	if err := exp.Export(context.Background(), &collectortrace.ExportTraceServiceRequest{}); err != nil {
		t.Fatal(err)
	}
	if !sawRequest {
		t.Fatal("server did not receive request")
	}
}

func TestExporterFailsOnNon2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer server.Close()

	exp := New(server.URL, time.Second)
	if err := exp.Export(context.Background(), &collectortrace.ExportTraceServiceRequest{}); err == nil {
		t.Fatal("expected error")
	}
}

func mustReadBody(t *testing.T, r *http.Request) []byte {
	t.Helper()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
