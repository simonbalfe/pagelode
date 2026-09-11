package patchright

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/simonbalfe/pagelode/internal/page"
)

const maxResponseBytes = 12 << 20

var errClosed = errors.New("patchright: client is closed")

type Client struct {
	command  string
	args     []string
	proxyURL string

	mu      sync.Mutex
	process *workerProcess
	pending map[string]chan workerResult
	closed  bool
	nextID  atomic.Uint64
}

type workerProcess struct {
	command *exec.Cmd
	input   io.WriteCloser
	encoder *json.Encoder
	done    chan struct{}
}

type workerResult struct {
	envelope renderEnvelope
	err      error
}

func New(command string, args []string, proxyURL string) (*Client, error) {
	if command == "" {
		return nil, errors.New("patchright: worker command is required")
	}
	return &Client{
		command:  command,
		args:     append([]string(nil), args...),
		proxyURL: proxyURL,
		pending:  make(map[string]chan workerResult),
	}, nil
}

func (c *Client) Fetch(ctx context.Context, targetURL string, session page.Session) (page.Document, error) {
	id := strconv.FormatUint(c.nextID.Add(1), 10)
	result := make(chan workerResult, 1)

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return page.Document{}, errClosed
	}
	if err := c.startLocked(); err != nil {
		c.mu.Unlock()
		return page.Document{}, err
	}
	c.pending[id] = result
	request := renderRequest{
		ID:              id,
		URL:             targetURL,
		TimeoutMS:       45_000,
		WaitUntil:       "domcontentloaded",
		UserAgent:       session.UserAgent,
		Cookies:         session.Cookies,
		ProxyURL:        c.proxyURL,
		SettleChallenge: true,
	}
	if err := c.process.encoder.Encode(request); err != nil {
		delete(c.pending, id)
		process := c.process
		c.mu.Unlock()
		_ = process.command.Process.Kill()
		return page.Document{}, fmt.Errorf("patchright: write worker request: %w", err)
	}
	c.mu.Unlock()

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return page.Document{}, fmt.Errorf("patchright: wait for worker: %w", ctx.Err())
	case response := <-result:
		if response.err != nil {
			return page.Document{}, response.err
		}
		return document(response.envelope, session)
	}
}

func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	process := c.process
	c.process = nil
	pending := c.takePendingLocked()
	c.mu.Unlock()

	fail(pending, errClosed)
	if process == nil {
		return nil
	}
	if err := process.input.Close(); err != nil {
		_ = process.command.Process.Kill()
		return fmt.Errorf("patchright: close worker input: %w", err)
	}
	select {
	case <-process.done:
		return nil
	case <-time.After(5 * time.Second):
		if err := process.command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("patchright: kill worker: %w", err)
		}
		<-process.done
		return nil
	}
}

func (c *Client) startLocked() error {
	if c.process != nil {
		return nil
	}
	command := exec.Command(c.command, c.args...)
	input, err := command.StdinPipe()
	if err != nil {
		return fmt.Errorf("patchright: create worker input: %w", err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		_ = input.Close()
		return fmt.Errorf("patchright: create worker output: %w", err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		_ = input.Close()
		return fmt.Errorf("patchright: start worker: %w", err)
	}
	process := &workerProcess{
		command: command,
		input:   input,
		encoder: json.NewEncoder(input),
		done:    make(chan struct{}),
	}
	c.process = process
	go c.read(process, output)
	return nil
}

func (c *Client) read(process *workerProcess, output io.Reader) {
	decoder := json.NewDecoder(output)
	var readErr error
	for {
		var envelope renderEnvelope
		if err := decoder.Decode(&envelope); err != nil {
			if !errors.Is(err, io.EOF) {
				readErr = fmt.Errorf("patchright: decode worker response: %w", err)
			}
			break
		}
		if envelope.ID == "" {
			readErr = errors.New("patchright: worker response omitted id")
			_ = process.command.Process.Kill()
			break
		}
		if envelope.Data != nil && len(envelope.Data.HTML) > maxResponseBytes {
			c.deliver(envelope.ID, workerResult{err: fmt.Errorf("patchright: response exceeds %d bytes", maxResponseBytes)})
			continue
		}
		c.deliver(envelope.ID, workerResult{envelope: envelope})
	}

	waitErr := process.command.Wait()
	if readErr == nil && waitErr != nil {
		readErr = fmt.Errorf("patchright: worker exited: %w", waitErr)
	}
	if readErr == nil {
		readErr = errors.New("patchright: worker exited")
	}

	c.mu.Lock()
	if c.process == process {
		c.process = nil
	}
	pending := c.takePendingLocked()
	c.mu.Unlock()
	fail(pending, readErr)
	close(process.done)
}

func (c *Client) deliver(id string, result workerResult) {
	c.mu.Lock()
	pending, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	c.mu.Unlock()
	if ok {
		pending <- result
	}
}

func (c *Client) takePendingLocked() []chan workerResult {
	result := make([]chan workerResult, 0, len(c.pending))
	for id, pending := range c.pending {
		result = append(result, pending)
		delete(c.pending, id)
	}
	return result
}

func fail(pending []chan workerResult, err error) {
	for _, result := range pending {
		result <- workerResult{err: err}
	}
}

func document(envelope renderEnvelope, session page.Session) (page.Document, error) {
	if !envelope.OK {
		if envelope.Error == nil {
			return page.Document{}, errors.New("patchright: worker returned an unspecified error")
		}
		return page.Document{}, fmt.Errorf("patchright: %s: %s", envelope.Error.Code, envelope.Error.Message)
	}
	if envelope.Data == nil {
		return page.Document{}, errors.New("patchright: worker response omitted data")
	}
	if envelope.Data.UserAgent != "" {
		session.UserAgent = envelope.Data.UserAgent
	}
	session.Cookies = envelope.Data.Cookies
	return page.Document{
		Provider:   page.ProviderPatchright,
		StatusCode: envelope.Data.StatusCode,
		FinalURL:   envelope.Data.FinalURL,
		Title:      envelope.Data.Title,
		HTML:       envelope.Data.HTML,
		Type:       page.ContentHTML,
		Session:    session,
	}, nil
}

type renderRequest struct {
	ID              string        `json:"id"`
	URL             string        `json:"url"`
	TimeoutMS       int           `json:"timeout_ms"`
	WaitUntil       string        `json:"wait_until"`
	UserAgent       string        `json:"user_agent,omitempty"`
	Cookies         []page.Cookie `json:"cookies,omitempty"`
	ProxyURL        string        `json:"proxy_url,omitempty"`
	SettleChallenge bool          `json:"settle_challenge"`
}

type renderEnvelope struct {
	ID    string       `json:"id"`
	OK    bool         `json:"ok"`
	Data  *renderData  `json:"data,omitempty"`
	Error *renderError `json:"error,omitempty"`
}

type renderData struct {
	StatusCode int           `json:"status_code"`
	FinalURL   string        `json:"final_url"`
	Title      string        `json:"title"`
	HTML       string        `json:"html"`
	UserAgent  string        `json:"user_agent"`
	Cookies    []page.Cookie `json:"cookies"`
}

type renderError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
