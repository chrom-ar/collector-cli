package sourcemaps

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"uuid"
)

// DefaultTestEventTimeout is how long SendTestEvent waits for Collector.
const DefaultTestEventTimeout = 10 * time.Second

// TestEventOptions say where to send the test event and what to put in it.
type TestEventOptions struct {
	// DSN is the project's DSN (--dsn or COLLECTOR_DSN). Its key is sent
	// as the bearer token, never shown.
	DSN string
	// Environment and Release, when set, are sent as the event's
	// (COLLECTOR_ENVIRONMENT and COLLECTOR_RELEASE).
	Environment string
	Release     string
	// Version is the CLI's version, sent as the SDK's.
	Version string
	// Hostname is sent as the event's server; "" means os.Hostname().
	Hostname string
	// HTTPClient sends the request; nil means a new http.Client.
	HTTPClient *http.Client
	// Timeout bounds the request; 0 means DefaultTestEventTimeout.
	Timeout time.Duration
	// UserAgent, when set, is sent as User-Agent.
	UserAgent string
}

// TestEventResult is an accepted test event.
type TestEventResult struct {
	// Status is Collector's answer, a 2xx.
	Status int
	// UUID is the event's id, to look for in Collector.
	UUID string
}

// String is the line to print on success.
func (r TestEventResult) String() string {
	return fmt.Sprintf("Test event sent (%d). Look for \"CollectorTest\" in your project: %s", r.Status, r.UUID)
}

// RejectedError is Collector's refusal of the test event.
type RejectedError struct {
	// Status is Collector's answer.
	Status int
}

func (e *RejectedError) Error() string {
	status := strconv.Itoa(e.Status)
	switch {
	case e.Status == http.StatusUnauthorized:
		return "Collector rejected the key (HTTP 401). Check the DSN on your project's setup page."
	case e.Status == http.StatusTooManyRequests:
		return "Collector asked to slow down (HTTP 429). Try again in a minute."
	case e.Status >= 500:
		return "Collector had an error (HTTP " + status + "). Try again in a minute."
	}
	return "Collector rejected the event (HTTP " + status + ")."
}

type testEvent struct {
	V           int               `json:"v"`
	UUID        string            `json:"uuid"`
	Timestamp   string            `json:"timestamp"`
	Platform    string            `json:"platform"`
	Level       string            `json:"level"`
	Server      string            `json:"server"`
	Environment string            `json:"environment,omitempty"`
	Release     string            `json:"release,omitempty"`
	Exception   map[string]string `json:"exception"`
	Tags        map[string]string `json:"tags"`
	Fingerprint []string          `json:"fingerprint"`
	SDK         map[string]string `json:"sdk"`
}

// SendTestEvent sends one event the way a server SDK does, to POST
// <DSN without the key>/api/events, so a person can see their setup works.
//
// A missing or invalid DSN is a *UsageError (exit 2). Collector refusing
// the event is a *RejectedError, and not reaching it another error (exit 1).
// Every error's message is ready to show after "error: ", without the key.
func SendTestEvent(ctx context.Context, opts TestEventOptions) (TestEventResult, error) {
	if opts.DSN == "" {
		return TestEventResult{}, newUsageError("missing --dsn (or COLLECTOR_DSN). Copy the DSN from your project's setup page.")
	}
	u, err := parseDSN(opts.DSN)
	if err != nil || u.User == nil || u.User.Username() == "" {
		return TestEventResult{}, newUsageError("invalid DSN: expected something like https://clt_…@app.clt.chrom.ar (copy it from your project's setup page).")
	}
	key := u.User.Username()
	base := origin(u) + strings.TrimRight(u.EscapedPath(), "/")

	host := opts.Hostname
	if host == "" {
		host, _ = os.Hostname()
	}
	event := testEvent{
		V:           1,
		UUID:        uuid.NewV4().String(),
		Timestamp:   time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		Platform:    "node",
		Level:       "error",
		Server:      host,
		Environment: opts.Environment,
		Release:     opts.Release,
		Exception: map[string]string{
			"type":    "CollectorTest",
			"message": "Test event from collector-cli test. If you can see it in Collector, your setup works.",
		},
		Tags:        map[string]string{"collector.test": "true"},
		Fingerprint: []string{"collector.test"},
		SDK:         map[string]string{"name": "@chrom-ar/collector-cli", "version": opts.Version},
	}
	body, err := json.Marshal(event)
	if err != nil {
		return TestEventResult{}, fmt.Errorf("encoding the test event: %w", err)
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTestEventTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/events", bytes.NewReader(body))
	if err != nil {
		return TestEventResult{}, newUsageError("invalid DSN: expected something like https://clt_…@app.clt.chrom.ar (copy it from your project's setup page).")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	if opts.UserAgent != "" {
		req.Header.Set("User-Agent", opts.UserAgent)
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	resp, err := client.Do(req)
	if err != nil {
		return TestEventResult{}, &unreachableError{host: strings.TrimPrefix(origin(u), u.Scheme+"://"), timeout: timeout, key: key, err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBody))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return TestEventResult{}, &RejectedError{Status: resp.StatusCode}
	}
	return TestEventResult{Status: resp.StatusCode, UUID: event.UUID}, nil
}

// unreachableError: the test event got no answer. It unwraps to the
// cause; its text never carries the key.
type unreachableError struct {
	host    string
	timeout time.Duration
	key     string
	err     error
}

func (e *unreachableError) Error() string {
	cause := networkDetail(e.err)
	var ne net.Error
	if errors.Is(e.err, context.DeadlineExceeded) || (errors.As(e.err, &ne) && ne.Timeout()) {
		cause = "no answer within " + seconds(e.timeout)
	}
	return scrub(fmt.Sprintf("couldn't reach Collector at %s (%s). Check the DSN's host and your network.", e.host, cause), e.key)
}

func (e *unreachableError) Unwrap() error { return e.err }
