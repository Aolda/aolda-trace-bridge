package helper

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type Client struct {
	command          []string
	connectionString string
	requestTimeout   time.Duration

	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr *boundedBuffer
	done   chan error
}

type Request struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	BaseID string `json:"base_id,omitempty"`
	Cursor string `json:"cursor,omitempty"`
	Count  int    `json:"count,omitempty"`
}

type Response struct {
	ID         string          `json:"id"`
	OK         bool            `json:"ok"`
	Report     json.RawMessage `json:"report,omitempty"`
	Traces     []TraceSummary  `json:"traces,omitempty"`
	NextCursor string          `json:"next_cursor,omitempty"`
	Deleted    int             `json:"deleted,omitempty"`
	Error      *ResponseError  `json:"error,omitempty"`
}

type TraceSummary struct {
	BaseID    string `json:"base_id"`
	Timestamp string `json:"timestamp,omitempty"`
}

type ResponseError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func NewClient(command []string, connectionString string, requestTimeout time.Duration) *Client {
	return &Client{
		command:          append([]string(nil), command...),
		connectionString: connectionString,
		requestTimeout:   requestTimeout,
		stderr:           newBoundedBuffer(32 * 1024),
	}
}

func (c *Client) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.startLocked()
}

func (c *Client) startLocked() error {
	if c.cmd != nil {
		return nil
	}
	if len(c.command) == 0 {
		return errors.New("helper command is required")
	}

	cmd := exec.Command(c.command[0], c.command[1:]...)
	cmd.Env = append(os.Environ(), "OSPROFILER_CONNECTION_STRING="+c.connectionString)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("helper stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("helper stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("helper stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start helper: %w", err)
	}

	c.cmd = cmd
	c.stdin = stdin
	c.stdout = bufio.NewReader(stdout)
	c.done = make(chan error, 1)

	go func() {
		_, _ = io.Copy(c.stderr, stderr)
	}()
	go func() {
		c.done <- cmd.Wait()
	}()

	return nil
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.closeLocked()
	return nil
}

func (c *Client) closeLocked() {
	if c.cmd == nil {
		return
	}
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
	}
	c.clearLocked()
}

func (c *Client) clearLocked() {
	c.cmd = nil
	c.stdin = nil
	c.stdout = nil
	c.done = nil
}

func (c *Client) GetReport(ctx context.Context, baseID string) (json.RawMessage, error) {
	if baseID == "" {
		return nil, errors.New("base_id is required")
	}
	resp, err := c.request(ctx, Request{
		ID:     "1",
		Method: "get_report",
		BaseID: baseID,
	}, "get_report")
	if err != nil {
		return nil, err
	}
	if len(resp.Report) == 0 {
		return nil, errors.New("helper returned empty report")
	}
	return resp.Report, nil
}

func (c *Client) ListTraces(ctx context.Context) ([]TraceSummary, error) {
	page, err := c.ListTracePage(ctx, "", 0)
	if err != nil {
		return nil, err
	}
	return page.Traces, nil
}

type TracePage struct {
	Traces     []TraceSummary
	NextCursor string
}

func (c *Client) ListTracePage(ctx context.Context, cursor string, count int) (TracePage, error) {
	resp, err := c.request(ctx, Request{
		ID:     "1",
		Method: "list_traces",
		Cursor: cursor,
		Count:  count,
	}, "list_traces")
	if err != nil {
		return TracePage{}, err
	}
	return TracePage{Traces: resp.Traces, NextCursor: resp.NextCursor}, nil
}

func (c *Client) DeleteTrace(ctx context.Context, baseID string) (int, error) {
	if baseID == "" {
		return 0, errors.New("base_id is required")
	}
	resp, err := c.request(ctx, Request{
		ID:     "1",
		Method: "delete_trace",
		BaseID: baseID,
	}, "delete_trace")
	if err != nil {
		return 0, err
	}
	return resp.Deleted, nil
}

func (c *Client) request(ctx context.Context, req Request, operation string) (Response, error) {
	if c.requestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.requestTimeout)
		defer cancel()
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.startLocked(); err != nil {
		return Response{}, err
	}

	line, err := json.Marshal(req)
	if err != nil {
		return Response{}, fmt.Errorf("marshal helper request: %w", err)
	}
	line = append(line, '\n')

	if _, err := c.stdin.Write(line); err != nil {
		c.closeLocked()
		return Response{}, fmt.Errorf("write helper request: %w%s", err, c.stderrSuffix())
	}

	respCh := make(chan responseResult, 1)
	go func() {
		resp, err := c.readResponse()
		respCh <- responseResult{resp: resp, err: err}
	}()

	select {
	case <-ctx.Done():
		c.closeLocked()
		return Response{}, fmt.Errorf("helper %s timed out: %w%s", operation, ctx.Err(), c.stderrSuffix())
	case err := <-c.done:
		if err == nil {
			err = errors.New("helper exited")
		}
		c.clearLocked()
		return Response{}, fmt.Errorf("helper exited before response: %w%s", err, c.stderrSuffix())
	case result := <-respCh:
		if result.err != nil {
			c.closeLocked()
			return Response{}, result.err
		}
		if result.resp.ID != req.ID {
			return Response{}, fmt.Errorf("helper response id mismatch: got %q want %q", result.resp.ID, req.ID)
		}
		if !result.resp.OK {
			if result.resp.Error == nil {
				return Response{}, errors.New("helper returned error without details")
			}
			return Response{}, fmt.Errorf("helper %s: %s", result.resp.Error.Code, result.resp.Error.Message)
		}
		return result.resp, nil
	}
}

type responseResult struct {
	resp Response
	err  error
}

func (c *Client) readResponse() (Response, error) {
	line, err := c.stdout.ReadBytes('\n')
	if err != nil {
		return Response{}, fmt.Errorf("read helper response: %w%s", err, c.stderrSuffix())
	}
	var resp Response
	if err := json.Unmarshal(bytes.TrimSpace(line), &resp); err != nil {
		return Response{}, fmt.Errorf("decode helper response: %w", err)
	}
	return resp, nil
}

func (c *Client) stderrSuffix() string {
	stderr := strings.TrimSpace(c.stderr.String())
	if stderr == "" {
		return ""
	}
	return "\nhelper stderr:\n" + stderr
}

type boundedBuffer struct {
	mu    sync.Mutex
	limit int
	buf   []byte
}

func newBoundedBuffer(limit int) *boundedBuffer {
	return &boundedBuffer{limit: limit}
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.buf = append(b.buf, p...)
	if len(b.buf) > b.limit {
		b.buf = append([]byte(nil), b.buf[len(b.buf)-b.limit:]...)
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(append([]byte(nil), b.buf...))
}
