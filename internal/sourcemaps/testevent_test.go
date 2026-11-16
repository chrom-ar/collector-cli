package sourcemaps

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// Ported from the `test` cases of the TypeScript CLI's test/run.test.ts.
// Exit codes are the command's: SendTestEvent returns a *UsageError (exit
// 2), a *RejectedError or another error (exit 1), or a result (exit 0).

type gotEvent struct {
	URL    string
	Header http.Header
	Body   map[string]any
}

type eventServer struct {
	*httptest.Server
	mu  sync.Mutex
	got []gotEvent
}

func (s *eventServer) events() []gotEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]gotEvent(nil), s.got...)
}

func (s *eventServer) dsn(key string) string {
	return strings.Replace(s.URL, "http://", "http://"+key+"@", 1)
}

func startEventServer(t *testing.T, status int) *eventServer {
	t.Helper()
	s := &eventServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(data, &body); err != nil {
			t.Errorf("body isn't JSON: %v", err)
		}
		s.mu.Lock()
		s.got = append(s.got, gotEvent{URL: r.URL.Path, Header: r.Header.Clone(), Body: body})
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status == http.StatusAccepted {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": body["uuid"]})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid key"})
	}))
	t.Cleanup(s.Close)
	return s
}

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestSendTestEventSendsABearerEventAndPrintsTheID(t *testing.T) {
	srv := startEventServer(t, http.StatusAccepted)
	res, err := SendTestEvent(context.Background(), TestEventOptions{
		DSN: srv.dsn("clt_k"), HTTPClient: srv.Client(), Hostname: "box", Version: "1.2.3", UserAgent: "collector/1.2.3",
	})
	if err != nil {
		t.Fatal(err)
	}
	events := srv.events()
	if len(events) != 1 {
		t.Fatalf("%d events", len(events))
	}
	ev := events[0]
	if ev.URL != "/api/events" || ev.Header.Get("Authorization") != "Bearer clt_k" || ev.Header.Get("Origin") != "" ||
		ev.Header.Get("Content-Type") != "application/json" || ev.Header.Get("User-Agent") != "collector/1.2.3" {
		t.Errorf("request = %+v", ev)
	}
	b := ev.Body
	exception, _ := b["exception"].(map[string]any)
	tags, _ := b["tags"].(map[string]any)
	sdk, _ := b["sdk"].(map[string]any)
	fp, _ := b["fingerprint"].([]any)
	if b["v"] != 1.0 || b["platform"] != "node" || b["level"] != "error" || b["server"] != "box" ||
		exception["type"] != "CollectorTest" ||
		exception["message"] != "Test event from collector-cli test. If you can see it in Collector, your setup works." ||
		tags["collector.test"] != "true" || len(fp) != 1 || fp[0] != "collector.test" ||
		sdk["name"] != "@chrom-ar/collector-cli" || sdk["version"] != "1.2.3" {
		t.Errorf("body = %+v", b)
	}
	if _, ok := b["environment"]; ok {
		t.Errorf("environment sent without one: %+v", b)
	}
	if _, ok := b["release"]; ok {
		t.Errorf("release sent without one: %+v", b)
	}
	ts, _ := b["timestamp"].(string)
	if _, err := time.Parse("2006-01-02T15:04:05.000Z", ts); err != nil {
		t.Errorf("timestamp = %q: %v", ts, err)
	}
	if !uuidV4.MatchString(res.UUID) || b["uuid"] != res.UUID {
		t.Errorf("uuid = %q, sent %v", res.UUID, b["uuid"])
	}
	if res.Status != 202 {
		t.Errorf("status = %d", res.Status)
	}
	want := `Test event sent (202). Look for "CollectorTest" in your project: ` + res.UUID
	if res.String() != want {
		t.Errorf("message = %q, want %q", res.String(), want)
	}
}

func TestSendTestEventCarriesEnvironmentAndRelease(t *testing.T) {
	srv := startEventServer(t, http.StatusAccepted)
	if _, err := SendTestEvent(context.Background(), TestEventOptions{
		DSN: srv.dsn("clt_k"), HTTPClient: srv.Client(), Environment: "staging", Release: "v7",
	}); err != nil {
		t.Fatal(err)
	}
	if b := srv.events()[0].Body; b["environment"] != "staging" || b["release"] != "v7" {
		t.Errorf("body = %+v", b)
	}
}

func TestSendTestEventKeepsTheDSNPath(t *testing.T) {
	srv := startEventServer(t, http.StatusAccepted)
	if _, err := SendTestEvent(context.Background(), TestEventOptions{
		DSN: srv.dsn("clt_k") + "/collector//?x=1#y", HTTPClient: srv.Client(),
	}); err != nil {
		t.Fatal(err)
	}
	if got := srv.events()[0].URL; got != "/collector/api/events" {
		t.Errorf("url = %q", got)
	}
}

func TestSendTestEventRejected(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   string
	}{
		{401, "Collector rejected the key (HTTP 401). Check the DSN on your project's setup page."},
		{429, "Collector asked to slow down (HTTP 429). Try again in a minute."},
		{500, "Collector had an error (HTTP 500). Try again in a minute."},
		{503, "Collector had an error (HTTP 503). Try again in a minute."},
		{400, "Collector rejected the event (HTTP 400)."},
		{301, "Collector rejected the event (HTTP 301)."},
	} {
		srv := startEventServer(t, tc.status)
		dsn := srv.dsn("clt_supersecret")
		client := srv.Client()
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		_, err := SendTestEvent(context.Background(), TestEventOptions{DSN: dsn, HTTPClient: client})
		var rejected *RejectedError
		if !errors.As(err, &rejected) || rejected.Status != tc.status || err.Error() != tc.want {
			t.Errorf("%d: err = %v, want %q", tc.status, err, tc.want)
		}
		if errors.Is(err, ErrUsage) {
			t.Errorf("%d: a rejection is not a usage error", tc.status)
		}
		// "test output never contains the key"
		if strings.Contains(err.Error(), "supersecret") {
			t.Errorf("key shown: %q", err)
		}
	}
}

func TestSendTestEventUnreachable(t *testing.T) {
	srv := startEventServer(t, http.StatusAccepted)
	dsn := srv.dsn("clt_supersecret")
	host := strings.TrimPrefix(srv.URL, "http://")
	srv.Close()
	_, err := SendTestEvent(context.Background(), TestEventOptions{DSN: dsn})
	if err == nil || errors.Is(err, ErrUsage) {
		t.Fatalf("err = %v, want a failure", err)
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "couldn't reach Collector at "+host+" (") ||
		!strings.HasSuffix(msg, "). Check the DSN's host and your network.") {
		t.Errorf("err = %q", msg)
	}
	if strings.Contains(msg, "supersecret") {
		t.Errorf("key shown: %q", msg)
	}
}

func TestSendTestEventTimesOut(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(block)
	_, err := SendTestEvent(context.Background(), TestEventOptions{
		DSN: strings.Replace(srv.URL, "http://", "http://clt_k@", 1), HTTPClient: srv.Client(), Timeout: 50 * time.Millisecond,
	})
	if err == nil || !strings.HasPrefix(err.Error(), "couldn't reach Collector at") || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v", err)
	}
}

func TestSendTestEventUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		name, dsn, want string
	}{
		{"without a DSN", "", "missing --dsn (or COLLECTOR_DSN). Copy the DSN from your project's setup page."},
		{"a malformed % in the DSN", "http://clt_%zz@127.0.0.1:9", "invalid DSN: expected something like https://clt_…@app.clt.chrom.ar (copy it from your project's setup page)."},
		{"no key", "http://127.0.0.1:9", "invalid DSN"},
		{"not a URL", "nope", "invalid DSN"},
		{"another scheme", "ftp://clt_k@127.0.0.1:9", "invalid DSN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := SendTestEvent(context.Background(), TestEventOptions{DSN: tc.dsn})
			if !errors.Is(err, ErrUsage) || !strings.HasPrefix(err.Error(), tc.want) {
				t.Errorf("err = %v, want a usage error %q", err, tc.want)
			}
		})
	}
}
