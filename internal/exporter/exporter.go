package exporter

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

type Exporter struct {
	endpoint string
	timeout  time.Duration
	client   *http.Client
}

func New(endpoint string, timeout time.Duration) Exporter {
	return Exporter{
		endpoint: endpoint,
		timeout:  timeout,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

func (e Exporter) Export(ctx context.Context, req *collectortrace.ExportTraceServiceRequest) error {
	if req == nil {
		return fmt.Errorf("otlp request is nil")
	}
	data, err := proto.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal otlp request: %w", err)
	}

	if e.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.timeout)
		defer cancel()
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("build otlp request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-protobuf")

	resp, err := e.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("post otlp traces to %s: %w", endpointHost(e.endpoint), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("post otlp traces to %s failed: status=%d body=%q", endpointHost(e.endpoint), resp.StatusCode, string(body))
	}
	return nil
}

func endpointHost(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return "<invalid-endpoint>"
	}
	return parsed.Host
}
