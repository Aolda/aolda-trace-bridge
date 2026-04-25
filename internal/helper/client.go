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
}

type Response struct {
	ID     string          `json:"id"`
	OK     bool            `json:"ok"`
	Report json.RawMessage `json:"report,omitempty"`
	Error  *ResponseError  `json:"error,omitempty"`
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

	if c.cmd == nil {
		return nil
	}
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
	}
	c.cmd = nil
	return nil
}

func (c *Client) GetReport(ctx context.Context, baseID string) (json.RawMessage, error) {
	if baseID == "" {
		return nil, errors.New("base_id is required")
	}
	if c.requestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.requestTimeout)
		defer cancel()
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cmd == nil {
		return nil, errors.New("helper is not started")
	}

	req := Request{
		ID:     "1",
		Method: "get_report",
		BaseID: baseID,
	}
	line, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal helper request: %w", err)
	}
	line = append(line, '\n')

	if _, err := c.stdin.Write(line); err != nil {
		return nil, fmt.Errorf("write helper request: %w%s", err, c.stderrSuffix())
	}

	respCh := make(chan responseResult, 1)
	go func() {
		resp, err := c.readResponse()
		respCh <- responseResult{resp: resp, err: err}
	}()

	select {
	case <-ctx.Done():
		if c.cmd != nil && c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		return nil, fmt.Errorf("helper get_report timed out: %w%s", ctx.Err(), c.stderrSuffix())
	case err := <-c.done:
		if err == nil {
			err = errors.New("helper exited")
		}
		return nil, fmt.Errorf("helper exited before response: %w%s", err, c.stderrSuffix())
	case result := <-respCh:
		if result.err != nil {
			return nil, result.err
		}
		if result.resp.ID != req.ID {
			return nil, fmt.Errorf("helper response id mismatch: got %q want %q", result.resp.ID, req.ID)
		}
		if !result.resp.OK {
			if result.resp.Error == nil {
				return nil, errors.New("helper returned error without details")
			}
			return nil, fmt.Errorf("helper %s: %s", result.resp.Error.Code, result.resp.Error.Message)
		}
		if len(result.resp.Report) == 0 {
			return nil, errors.New("helper returned empty report")
		}
		return result.resp.Report, nil
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
