package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chrom-ar/collector-cli/internal/channel/channeltest"
)

// syncBuffer is a bytes.Buffer safe to write from one goroutine and read
// from another.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

const followToken = "cltp_test_follow" //nolint:gosec // A fake token.

// logsREST answers GET …/logs with one page of records, newest first.
func logsREST(records ...string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/orgs/acme/projects/shop/logs") || r.Header.Get("Authorization") != "Bearer "+followToken {
			http.Error(w, `{"error":{"code":"not_found","message":"No."}}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[`+strings.Join(records, ",")+`],"next_cursor":null}`)
	})
}

type followRun struct {
	stdout, stderr *syncBuffer
	code           chan int
	cancel         context.CancelFunc
}

func startFollow(t *testing.T, srv *channeltest.Server, args ...string) *followRun {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	r := &followRun{stdout: &syncBuffer{}, stderr: &syncBuffer{}, code: make(chan int, 1), cancel: cancel}
	env := map[string]string{"COLLECTOR_URL": srv.URL, "COLLECTOR_TOKEN": followToken, "COLLECTOR_ORG": "acme", "COLLECTOR_PROJECT": "shop"}
	go func() {
		r.code <- Run(ctx, append([]string{"logs", "-f"}, args...), Env{
			Stdout: r.stdout, Stderr: r.stderr,
			Getenv:     func(k string) string { return env[k] },
			ConfigDir:  t.TempDir(),
			Now:        func() time.Time { return time.Date(2026, 11, 13, 12, 5, 0, 0, time.UTC) },
			Location:   time.UTC,
			HTTPClient: &http.Client{Transport: &http.Transport{}},
			Sleep: func(ctx context.Context, _ time.Duration) error {
				return ctx.Err()
			},
		})
	}()
	t.Cleanup(cancel)
	return r
}

func (r *followRun) wait(t *testing.T) int {
	t.Helper()
	select {
	case c := <-r.code:
		return c
	case <-time.After(10 * time.Second):
		t.Fatalf("collector logs -f didn't end; stderr: %s", r.stderr.String())
	}
	return -1
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestFollowPrintsBacklogThenLiveAsNDJSON(t *testing.T) {
	srv := channeltest.New(t, channeltest.Options{
		Token: followToken,
		REST: logsREST(
			`{"kind":"log","id":"b","at":"2026-11-13T12:00:02Z","level":"info","message":"two"}`,
			`{"kind":"log","id":"a","at":"2026-11-13T12:00:01Z","level":"info","message":"one"}`,
		),
	})
	r := startFollow(t, srv, "--json")
	if err := srv.WaitJoins(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the backlog", func() bool { return strings.Count(r.stdout.String(), "\n") == 2 })
	if err := srv.Push("records", map[string]any{"data": []map[string]any{
		{"kind": "log", "id": "b", "at": "2026-11-13T12:00:02Z", "message": "two again"},
		{"kind": "log", "id": "c", "at": "2026-11-13T12:00:03Z", "message": "three"},
	}, "skipped": 4}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the push", func() bool { return strings.Count(r.stdout.String(), "\n") == 3 })
	r.cancel()
	if code := r.wait(t); code != ExitOK {
		t.Errorf("exit %d after Ctrl-C, want 0; stderr: %s", code, r.stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(r.stdout.String()), "\n")
	var ids []string
	for _, l := range lines {
		var rec struct{ ID string }
		if err := json.Unmarshal([]byte(l), &rec); err != nil {
			t.Fatalf("not NDJSON: %q", l)
		}
		ids = append(ids, rec.ID)
	}
	if strings.Join(ids, ",") != "a,b,c" {
		t.Errorf("ids = %v, want a,b,c (oldest first, deduped)", ids)
	}
	if !strings.Contains(r.stderr.String(), "… 4 records skipped (too many to stream)") {
		t.Errorf("stderr = %q", r.stderr.String())
	}
	for _, c := range srv.Connections() {
		if strings.Contains(c.Path, followToken) {
			t.Error("the token went in the URL")
		}
	}
}

func TestFollowRevokedExitsThree(t *testing.T) {
	srv := channeltest.New(t, channeltest.Options{Token: followToken, REST: logsREST()})
	r := startFollow(t, srv)
	if err := srv.WaitJoins(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // Let the backlog read finish.
	srv.CloseWith(4001, "revoked")
	if code := r.wait(t); code != ExitAuth {
		t.Errorf("exit %d, want 3; stderr: %s", code, r.stderr.String())
	}
	if !strings.Contains(r.stderr.String(), "run: collector login") {
		t.Errorf("stderr = %q", r.stderr.String())
	}
	if strings.Contains(r.stderr.String()+r.stdout.String(), followToken) {
		t.Error("the token was printed")
	}
}

func TestFollowJoinRefused(t *testing.T) {
	tests := []struct {
		code string
		exit int
	}{{"not_found", ExitError}, {"invalid", ExitUsage}}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			srv := channeltest.New(t, channeltest.Options{
				Token: followToken,
				REST:  logsREST(),
				Join: func(string, json.RawMessage) channeltest.Reply {
					return channeltest.Reply{Status: "error", Response: map[string]any{"error": map[string]any{"code": tt.code, "message": "No logs here."}}}
				},
			})
			r := startFollow(t, srv)
			if code := r.wait(t); code != tt.exit {
				t.Errorf("exit %d, want %d; stderr: %s", code, tt.exit, r.stderr.String())
			}
			if !strings.Contains(r.stderr.String(), "No logs here.") {
				t.Errorf("stderr = %q", r.stderr.String())
			}
		})
	}
}

func TestRecordLines(t *testing.T) {
	a := &app{env: withDefaults(Env{
		Location: time.UTC,
		Now:      func() time.Time { return time.Date(2026, 11, 13, 0, 5, 0, 0, time.UTC) },
	})}
	p := a.recordPrinter(true)
	recs := decodeRecords([]json.RawMessage{
		[]byte(`{"kind":"log","id":"1","at":"2026-11-12T23:59:59.5Z","server":"web-1","level":"info","message":"before midnight"}`),
		[]byte(`{"kind":"log","id":"2","at":"2026-11-12T23:59:59.9Z","server":"web-2","level":"info","message":"same day"}`),
		[]byte(`{"kind":"sign_in","id":"3","at":"2026-11-13T00:00:01Z","outcome":"failure","account":"\u001b]52;c;aGk=\u0007x\rhidden"}`),
		[]byte(`{"kind":"log","id":"4","at":"2026-11-13T00:00:02Z","message":"` + strings.Repeat("x", 300) + `"}`),
		[]byte(`{"kind":"request","id":"5","at":"2026-11-13T00:00:03Z","method":"GET"}`),
	})
	var got []string
	for _, r := range recs {
		got = append(got, p.line(r))
	}
	want := []string{
		"2026-11-12 23:59:59.500 web-1 info before midnight",
		"23:59:59.900 web-2 info same day",
		"2026-11-13 00:00:01.000 sign_in failure �]52;c;aGk=�x⏎hidden",
		"00:00:02.000 " + strings.Repeat("x", 199) + "…",
		"00:00:03.000 GET",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q\nwant      %q", i, got[i], want[i])
		}
	}
}
