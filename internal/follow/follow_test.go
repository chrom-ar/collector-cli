package follow_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/chrom-ar/collector-cli/internal/api"
	"github.com/chrom-ar/collector-cli/internal/channel"
	"github.com/chrom-ar/collector-cli/internal/channel/channeltest"
	"github.com/chrom-ar/collector-cli/internal/follow"
)

const (
	topic = "logs:acme/shop"
	token = "cltp_follow_test" //nolint:gosec // A fake token.
	wait  = 5 * time.Second
)

// rec is a log record as REST and the channel send it.
func rec(id string, s int) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"kind":"log","id":%q,"at":"2026-11-11T11:00:%02d.000Z","message":"m-%s"}`, id, s, id))
}

func recs(rs ...json.RawMessage) []json.RawMessage { return rs }

// push is a records push's payload.
func push(skipped int, rs ...json.RawMessage) map[string]any {
	return map[string]any{"data": rs, "skipped": skipped}
}

// page is a REST answer.
type page struct {
	records []json.RawMessage
	next    string
	err     error
}

// fetcher is a fake REST API: answer decides each call's answer, given the
// call's query and its index. It blocks while hold is set.
type fetcher struct {
	answer func(ctx context.Context, q api.Query, n int) page

	mu    sync.Mutex
	calls []api.Query
}

func (f *fetcher) Logs(ctx context.Context, q api.Query) ([]json.RawMessage, string, error) {
	f.mu.Lock()
	n := len(f.calls)
	f.calls = append(f.calls, maps.Clone(q))
	f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	p := f.answer(ctx, q, n)
	if ctx.Err() != nil {
		return nil, "", ctx.Err()
	}
	return p.records, p.next, p.err
}

func (f *fetcher) Calls() []api.Query {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// pages answers the calls in order with the pages, the last one again and
// again.
func pages(ps ...page) *fetcher {
	return &fetcher{answer: func(_ context.Context, _ api.Query, n int) page { return ps[min(n, len(ps)-1)] }}
}

// blocking answers nothing until the call's ctx ends.
func blocking() *fetcher {
	return &fetcher{answer: func(ctx context.Context, _ api.Query, _ int) page { <-ctx.Done(); return page{err: ctx.Err()} }}
}

// run is one Run in the background, with what it printed and reported.
type run struct {
	t      *testing.T
	cancel context.CancelFunc
	done   chan struct{} // closed once Run has returned err
	err    error

	mu      sync.Mutex
	printed []string
	skipped []int
	retries []time.Duration
	causes  []error
}

func (r *run) Printed() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.printed)
}

func (r *run) Retries() []time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.retries)
}

// stop cancels the run and returns its error, failing if it takes longer
// than within.
func (r *run) stop(within time.Duration) error {
	r.t.Helper()
	started := time.Now()
	r.cancel()
	err := r.result()
	if d := time.Since(started); d > within {
		r.t.Errorf("Run took %s to stop, want under %s", d, within)
	}
	return err
}

// result waits for Run to return.
func (r *run) result() error {
	r.t.Helper()
	select {
	case <-r.done:
		return r.err
	case <-time.After(wait):
		r.t.Fatal("Run didn't return")
		return nil
	}
}

// noLeaks fails the test if goroutines started during it outlive it. Call
// it first, so its check runs after every other cleanup.
func noLeaks(t *testing.T) {
	t.Helper()
	before := runtime.NumGoroutine()
	t.Cleanup(func() {
		deadline := time.Now().Add(wait)
		for runtime.NumGoroutine() > before {
			if time.Now().After(deadline) {
				buf := make([]byte, 1<<20)
				t.Errorf("%d goroutines left behind:\n%s", runtime.NumGoroutine()-before, buf[:runtime.Stack(buf, true)])
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	})
}

// dialer dials url with the test's own HTTP client.
func dialer(t *testing.T, url string) func(context.Context, func(channel.Records)) (*channel.Conn, error) {
	t.Helper()
	tr := &http.Transport{}
	t.Cleanup(tr.CloseIdleConnections)
	return func(ctx context.Context, onRecords func(channel.Records)) (*channel.Conn, error) {
		return channel.Dial(ctx, channel.Options{
			URL: url, Token: token, HTTPClient: &http.Client{Transport: tr},
			ConnectTimeout: wait, JoinTimeout: wait, OnRecords: onRecords,
		})
	}
}

// noSleep returns at once, as if every backoff were over.
func noSleep(ctx context.Context, _ time.Duration) error { return ctx.Err() }

// sleepForever waits until ctx ends.
func sleepForever(ctx context.Context, _ time.Duration) error {
	<-ctx.Done()
	return ctx.Err()
}

// start runs follow.Run against url in the background. change edits the
// options first.
func start(t *testing.T, url string, f follow.Fetcher, change func(*follow.Options)) *run {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	r := &run{t: t, cancel: cancel, done: make(chan struct{})}
	o := follow.Options{
		Fetch: f,
		Dial:  dialer(t, url),
		URL:   url,
		Topic: topic,
		Print: func(rs []json.RawMessage) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			for _, raw := range rs {
				var v struct{ ID string }
				if err := json.Unmarshal(raw, &v); err != nil {
					t.Errorf("printed %s: %v", raw, err)
				}
				r.printed = append(r.printed, v.ID)
			}
			return nil
		},
		Skipped: func(n int) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.skipped = append(r.skipped, n)
		},
		Reconnecting: func(in time.Duration, cause error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.retries = append(r.retries, in)
			r.causes = append(r.causes, cause)
		},
		Sleep: noSleep,
	}
	if change != nil {
		change(&o)
	}
	go func() {
		defer close(r.done)
		r.err = follow.Run(ctx, o)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-r.done:
		case <-time.After(wait):
			t.Error("Run didn't return after cancel")
		}
	})
	return r
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(wait)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitJoins(t *testing.T, srv *channeltest.Server, n int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), wait)
	defer cancel()
	if err := srv.WaitJoins(ctx, n); err != nil {
		t.Fatal(err)
	}
}

func printedIs(t *testing.T, r *run, want ...string) {
	t.Helper()
	eventually(t, fmt.Sprintf("printed %v", want), func() bool { return len(r.Printed()) >= len(want) })
	time.Sleep(20 * time.Millisecond) // anything more would be a duplicate
	if got := r.Printed(); !slices.Equal(got, want) {
		t.Errorf("printed %v, want %v", got, want)
	}
}

func mustPush(t *testing.T, srv *channeltest.Server, payload any) {
	t.Helper()
	if err := srv.Push("records", payload); err != nil {
		t.Fatal(err)
	}
}

// closedURL is the URL of a server that is no longer there.
func closedURL(t *testing.T) string {
	t.Helper()
	srv := channeltest.New(t, channeltest.Options{})
	srv.Close()
	return srv.URL
}

// logs-follow.test.ts: "backlog then live pushes, oldest first; Ctrl-C
// exits 0".
func TestBacklogThenLivePushesOldestFirst(t *testing.T) {
	noLeaks(t)
	srv := channeltest.New(t, channeltest.Options{})
	f := pages(page{records: recs(rec("b", 2), rec("a", 1))})
	r := start(t, srv.URL, f, func(o *follow.Options) {
		o.Filters = map[string]string{"level": "warning"}
		o.Limit = "50"
	})
	waitJoins(t, srv, 1)
	printedIs(t, r, "a", "b")
	mustPush(t, srv, push(0, rec("c", 3), rec("d", 4)))
	printedIs(t, r, "a", "b", "c", "d")

	if err := r.stop(time.Second); !errors.Is(err, context.Canceled) {
		t.Errorf("Run = %v, want context.Canceled", err)
	}
	joins := 0
	for _, fr := range srv.Frames() {
		if fr.Event == "phx_join" {
			joins++
			if fr.Topic != topic || string(fr.Payload) != `{"level":"warning"}` {
				t.Errorf("join %s %s", fr.Topic, fr.Payload)
			}
		}
	}
	if joins != 1 {
		t.Errorf("%d joins", joins)
	}
	if calls := f.Calls(); len(calls) != 1 || !maps.Equal(calls[0], api.Query{"level": "warning", "limit": "50"}) {
		t.Errorf("REST calls %v", calls)
	}
}

// logs-follow.test.ts: "push held during backlog fetch is printed once,
// after the backlog".
func TestPushDuringBacklogIsHeldAndPrintedOnce(t *testing.T) {
	noLeaks(t)
	srv := channeltest.New(t, channeltest.Options{})
	release := make(chan struct{})
	f := &fetcher{answer: func(context.Context, api.Query, int) page {
		<-release
		return page{records: recs(rec("b", 2), rec("a", 1))}
	}}
	r := start(t, srv.URL, f, nil)
	waitJoins(t, srv, 1)
	eventually(t, "the backlog request", func() bool { return len(f.Calls()) == 1 })
	mustPush(t, srv, push(0, rec("b", 2), rec("c", 3)))
	time.Sleep(20 * time.Millisecond)
	if got := r.Printed(); len(got) != 0 {
		t.Errorf("printed %v before the backlog", got)
	}
	close(release)
	printedIs(t, r, "a", "b", "c")
}

// logs-follow.test.ts: "after a drop, gap filled from newest at, no
// duplicates".
func TestGapFilledAfterDropWithoutDuplicates(t *testing.T) {
	noLeaks(t)
	srv := channeltest.New(t, channeltest.Options{})
	f := pages(
		page{records: recs(rec("b", 2), rec("a", 1))},
		page{records: recs(rec("d", 4), rec("c", 3), rec("b", 2))},
	)
	r := start(t, srv.URL, f, func(o *follow.Options) { o.Filters = map[string]string{"level": "warning"} })
	waitJoins(t, srv, 1)
	printedIs(t, r, "a", "b")
	srv.Drop()
	waitJoins(t, srv, 2)
	printedIs(t, r, "a", "b", "c", "d")
	mustPush(t, srv, push(0, rec("d", 4), rec("e", 5)))
	printedIs(t, r, "a", "b", "c", "d", "e")

	calls := f.Calls()
	// A minute before the newest record shown, so late or clock-skewed
	// records aren't lost.
	if want := (api.Query{"level": "warning", "from": "2026-11-11T10:59:02.000Z", "limit": "100"}); len(calls) < 2 || !maps.Equal(calls[1], want) {
		t.Errorf("gap fill calls %v, want the second to be %v", calls, want)
	}
	if got := r.Retries(); !slices.Equal(got, []time.Duration{time.Second}) {
		t.Errorf("reconnecting in %v, want [1s]", got)
	}
	r.mu.Lock()
	cause := r.causes[0]
	r.mu.Unlock()
	if !errors.Is(cause, channel.ErrDropped) {
		t.Errorf("cause %v, want ErrDropped", cause)
	}
}

// logs-follow.test.ts: "stop during backoff exits 0".
func TestStopDuringBackoff(t *testing.T) {
	noLeaks(t)
	srv := channeltest.New(t, channeltest.Options{})
	r := start(t, srv.URL, pages(page{}), func(o *follow.Options) { o.Sleep = sleepForever })
	waitJoins(t, srv, 1)
	srv.Drop()
	eventually(t, "reconnecting", func() bool { return len(r.Retries()) == 1 })
	if err := r.stop(100 * time.Millisecond); !errors.Is(err, context.Canceled) {
		t.Errorf("Run = %v, want context.Canceled", err)
	}
}

// logs-follow.test.ts: "stop during backoff leaves no timer behind". Go
// can't count timers; the default Sleep is api.Sleep, which stops its
// timer, and the goroutine check covers the rest.
func TestStopDuringRealBackoffLeavesNothingBehind(t *testing.T) {
	noLeaks(t)
	srv := channeltest.New(t, channeltest.Options{})
	r := start(t, srv.URL, pages(page{}), func(o *follow.Options) { o.Sleep = nil })
	waitJoins(t, srv, 1)
	srv.Drop()
	eventually(t, "reconnecting", func() bool { return len(r.Retries()) == 1 })
	if err := r.stop(100 * time.Millisecond); !errors.Is(err, context.Canceled) {
		t.Errorf("Run = %v, want context.Canceled", err)
	}
}

// logs-follow.test.ts: "auth close exits 3 with run: collector login".
func TestRevokedCloseEnds(t *testing.T) {
	for _, code := range []int{4001, 4003} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			noLeaks(t)
			srv := channeltest.New(t, channeltest.Options{})
			r := start(t, srv.URL, pages(page{}), nil)
			waitJoins(t, srv, 1)
			srv.CloseWith(code, "revoked")
			if err := r.result(); !errors.Is(err, channel.ErrRevoked) {
				t.Errorf("Run = %v, want ErrRevoked", err)
			}
		})
	}
}

func TestJoinRefusedForAuthEnds(t *testing.T) {
	noLeaks(t)
	srv := channeltest.New(t, channeltest.Options{Join: func(string, json.RawMessage) channeltest.Reply {
		return channeltest.Reply{Status: "error", Response: map[string]any{"error": map[string]any{"code": "forbidden", "message": "No."}}}
	}})
	r := start(t, srv.URL, pages(page{}), nil)
	if err := r.result(); !errors.Is(err, channel.ErrRevoked) {
		t.Errorf("Run = %v, want ErrRevoked", err)
	}
}

// logs-follow.test.ts: "a refused connection that REST answers 401 exits
// 3", with nothing listening and with the upgrade refused (403).
func TestRefusedConnectionProbesREST(t *testing.T) {
	unauthorized := page{err: &api.Error{Status: http.StatusUnauthorized, Code: "unauthorized", Message: "Sign in again."}}
	t.Run("nothing listening", func(t *testing.T) {
		noLeaks(t)
		f := pages(unauthorized)
		r := start(t, closedURL(t), f, func(o *follow.Options) {
			o.Filters = map[string]string{"level": "warning", "from": "2026-11-11T10:00:00Z"}
		})
		if err := r.result(); !errors.Is(err, api.ErrUnauthorized) {
			t.Errorf("Run = %v, want api.ErrUnauthorized", err)
		}
		if calls := f.Calls(); len(calls) != 1 || !maps.Equal(calls[0], api.Query{"level": "warning", "limit": "1"}) {
			t.Errorf("probe calls %v", calls)
		}
	})
	t.Run("upgrade refused", func(t *testing.T) {
		noLeaks(t)
		srv := channeltest.New(t, channeltest.Options{Token: "cltp_someone_else"}) //nolint:gosec // A fake token.
		r := start(t, srv.URL, pages(unauthorized), nil)
		if err := r.result(); !errors.Is(err, api.ErrUnauthorized) {
			t.Errorf("Run = %v, want api.ErrUnauthorized", err)
		}
	})
}

// logs-follow.test.ts: "join refused not_found exits 1; invalid exits 2".
func TestJoinRefusedEnds(t *testing.T) {
	for _, code := range []string{"not_found", "invalid", "something_new"} {
		t.Run(code, func(t *testing.T) {
			noLeaks(t)
			srv := channeltest.New(t, channeltest.Options{Join: func(string, json.RawMessage) channeltest.Reply {
				return channeltest.Reply{Status: "error", Response: map[string]any{"error": map[string]any{"code": code, "message": "Bad " + code}}}
			}})
			r := start(t, srv.URL, pages(page{}), nil)
			err := r.result()
			var je *channel.JoinError
			if !errors.As(err, &je) || je.Code != code || je.Message != "Bad "+code {
				t.Errorf("Run = %v, want the JoinError", err)
			}
			if n := srv.Joins(); n != 0 {
				t.Errorf("%d joins", n)
			}
		})
	}
}

func TestJoinUnavailableRetries(t *testing.T) {
	noLeaks(t)
	var mu sync.Mutex
	refuse := true
	srv := channeltest.New(t, channeltest.Options{Join: func(string, json.RawMessage) channeltest.Reply {
		mu.Lock()
		defer mu.Unlock()
		if refuse {
			refuse = false
			return channeltest.Reply{Status: "error", Response: map[string]any{"error": map[string]any{"code": "unavailable", "message": "Later."}}}
		}
		return channeltest.Reply{Status: "ok"}
	}})
	r := start(t, srv.URL, pages(page{records: recs(rec("a", 1))}), nil)
	waitJoins(t, srv, 1)
	printedIs(t, r, "a")
	if got := r.Retries(); len(got) != 1 {
		t.Errorf("reconnecting %v, want once", got)
	}
}

// logs-follow.test.ts: "skipped > 0 in a records push says how many, on
// stderr".
func TestSkippedIsReported(t *testing.T) {
	noLeaks(t)
	srv := channeltest.New(t, channeltest.Options{})
	r := start(t, srv.URL, pages(page{}), nil)
	waitJoins(t, srv, 1)
	mustPush(t, srv, push(120, rec("a", 1)))
	printedIs(t, r, "a")
	r.mu.Lock()
	defer r.mu.Unlock()
	if !slices.Equal(r.skipped, []int{120}) {
		t.Errorf("skipped %v, want [120]", r.skipped)
	}
}

// logs-follow.test.ts: "a record older than the newest shown, arriving
// during a drop, is printed".
func TestLateRecordDuringDropIsPrinted(t *testing.T) {
	noLeaks(t)
	srv := channeltest.New(t, channeltest.Options{})
	f := pages(page{records: recs(rec("b", 10))}, page{records: recs(rec("b", 10), rec("late", 8))})
	r := start(t, srv.URL, f, nil)
	waitJoins(t, srv, 1)
	printedIs(t, r, "b")
	srv.Drop()
	waitJoins(t, srv, 2)
	printedIs(t, r, "b", "late")
}

// logs-follow.test.ts: "phx_close reconnects instead of failing".
func TestPhxCloseReconnects(t *testing.T) {
	for name, end := range map[string]func(*channeltest.Server) error{
		"phx_close": (*channeltest.Server).PhxClose,
		"phx_error": (*channeltest.Server).PhxError,
	} {
		t.Run(name, func(t *testing.T) {
			noLeaks(t)
			srv := channeltest.New(t, channeltest.Options{})
			r := start(t, srv.URL, pages(page{}), nil)
			waitJoins(t, srv, 1)
			if err := end(srv); err != nil {
				t.Fatal(err)
			}
			waitJoins(t, srv, 2)
			if err := r.stop(time.Second); !errors.Is(err, context.Canceled) {
				t.Errorf("Run = %v, want context.Canceled", err)
			}
		})
	}
}

// logs-follow.test.ts: "stop during the REST probe exits 0 at once".
func TestStopDuringProbe(t *testing.T) {
	noLeaks(t)
	f := blocking()
	r := start(t, closedURL(t), f, nil)
	eventually(t, "the probe", func() bool { return len(f.Calls()) == 1 })
	if err := r.stop(100 * time.Millisecond); !errors.Is(err, context.Canceled) {
		t.Errorf("Run = %v, want context.Canceled", err)
	}
}

func TestStopWhileConnecting(t *testing.T) {
	noLeaks(t)
	dialing := make(chan struct{})
	r := start(t, "http://127.0.0.1:1", pages(page{}), func(o *follow.Options) {
		o.Dial = func(ctx context.Context, _ func(channel.Records)) (*channel.Conn, error) {
			close(dialing)
			<-ctx.Done()
			return nil, ctx.Err()
		}
	})
	<-dialing
	if err := r.stop(100 * time.Millisecond); !errors.Is(err, context.Canceled) {
		t.Errorf("Run = %v, want context.Canceled", err)
	}
}

func TestStopDuringBacklog(t *testing.T) {
	noLeaks(t)
	srv := channeltest.New(t, channeltest.Options{})
	f := blocking()
	r := start(t, srv.URL, f, nil)
	eventually(t, "the backlog request", func() bool { return len(f.Calls()) == 1 })
	if err := r.stop(100 * time.Millisecond); !errors.Is(err, context.Canceled) {
		t.Errorf("Run = %v, want context.Canceled", err)
	}
}

// logs-follow.test.ts: "a permanent REST error while following ends it
// (404 → exit 1)".
func TestPermanentRESTErrorEnds(t *testing.T) {
	notFound := &api.Error{Status: http.StatusNotFound, Code: "not_found", Message: "Logs aren't available on this Collector."}
	t.Run("probe", func(t *testing.T) {
		noLeaks(t)
		r := start(t, closedURL(t), pages(page{err: notFound}), nil)
		if err := r.result(); !errors.Is(err, notFound) {
			t.Errorf("Run = %v, want the 404", err)
		}
	})
	t.Run("backlog", func(t *testing.T) {
		noLeaks(t)
		srv := channeltest.New(t, channeltest.Options{})
		r := start(t, srv.URL, pages(page{err: notFound}), nil)
		if err := r.result(); !errors.Is(err, notFound) {
			t.Errorf("Run = %v, want the 404", err)
		}
	})
}

func TestPassingRESTErrorsRetry(t *testing.T) {
	for _, status := range []int{0, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			noLeaks(t)
			srv := channeltest.New(t, channeltest.Options{})
			f := pages(page{err: &api.Error{Status: status, Code: "x", Message: "later"}}, page{records: recs(rec("a", 1))})
			r := start(t, srv.URL, f, nil)
			printedIs(t, r, "a")
			if got := r.Retries(); len(got) != 1 {
				t.Errorf("reconnecting %v, want once", got)
			}
			if n := srv.Joins(); n != 2 {
				t.Errorf("%d joins, want 2", n)
			}
		})
	}
}

// logs-follow.test.ts: "a stream that never opens gives up after 5 tries
// with a hint".
func TestGivesUpWhenTheStreamNeverOpens(t *testing.T) {
	noLeaks(t)
	url := closedURL(t)
	f := pages(page{})
	r := start(t, url, f, nil)
	err := r.result()
	if !errors.Is(err, follow.ErrNeverOpened) {
		t.Fatalf("Run = %v, want ErrNeverOpened", err)
	}
	if want := "couldn't open the live stream at " + url + "; this Collector may not support --follow yet"; err.Error() != want {
		t.Errorf("message %q, want %q", err, want)
	}
	if got := r.Retries(); !slices.Equal(got, []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}) {
		t.Errorf("reconnecting %v, want 4 backoffs", got)
	}
	if n := len(f.Calls()); n != 5 {
		t.Errorf("%d probes, want 5", n)
	}
}

func TestOnceOpenedRetriesForeverWithCappedBackoff(t *testing.T) {
	noLeaks(t)
	srv := channeltest.New(t, channeltest.Options{})
	var mu sync.Mutex
	dials := 0
	real := dialer(t, srv.URL)
	r := start(t, srv.URL, pages(page{}), func(o *follow.Options) {
		o.Dial = func(ctx context.Context, on func(channel.Records)) (*channel.Conn, error) {
			mu.Lock()
			dials++
			first := dials == 1
			mu.Unlock()
			if first {
				return real(ctx, on)
			}
			return nil, fmt.Errorf("refused: %w", channel.ErrDropped)
		}
	})
	waitJoins(t, srv, 1)
	srv.Drop()
	eventually(t, "8 backoffs", func() bool { return len(r.Retries()) >= 8 })
	if err := r.stop(time.Second); !errors.Is(err, context.Canceled) {
		t.Errorf("Run = %v, want context.Canceled", err)
	}
	s := time.Second
	if got, want := r.Retries()[:8], []time.Duration{s, 2 * s, 4 * s, 8 * s, 16 * s, 30 * s, 30 * s, 30 * s}; !slices.Equal(got, want) {
		t.Errorf("backoffs %v, want %v", got, want)
	}
}

func TestBackoffResetsOnceReopened(t *testing.T) {
	noLeaks(t)
	srv := channeltest.New(t, channeltest.Options{})
	f := pages(page{})
	r := start(t, srv.URL, f, nil)
	for i := 1; i <= 3; i++ {
		eventually(t, "the backlog", func() bool { return len(f.Calls()) == i })
		time.Sleep(10 * time.Millisecond)
		srv.Drop()
	}
	eventually(t, "3 backoffs", func() bool { return len(r.Retries()) == 3 })
	if got := r.Retries(); !slices.Equal(got, []time.Duration{time.Second, time.Second, time.Second}) {
		t.Errorf("backoffs %v, want 1s each", got)
	}
}

func TestSinceReadsEveryPageOldestFirst(t *testing.T) {
	noLeaks(t)
	srv := channeltest.New(t, channeltest.Options{})
	byCursor := map[string]page{
		"":   {records: recs(rec("f", 6), rec("e", 5)), next: "c1"},
		"c1": {records: recs(rec("d", 4), rec("c", 3)), next: "c2"},
		"c2": {records: recs(rec("b", 2), rec("a", 1))},
	}
	f := &fetcher{answer: func(_ context.Context, q api.Query, _ int) page { return byCursor[q["cursor"]] }}
	r := start(t, srv.URL, f, func(o *follow.Options) {
		o.Filters = map[string]string{"from": "2026-11-11T10:00:00Z", "level": "warning"}
	})
	printedIs(t, r, "a", "b", "c", "d", "e", "f")
	var cursors []string
	for _, q := range f.Calls() {
		if q["from"] != "2026-11-11T10:00:00Z" || q["level"] != "warning" || q["limit"] != "100" {
			t.Errorf("query %v", q)
		}
		cursors = append(cursors, q["cursor"])
	}
	// Walk the cursors, then read the pages back from the oldest: at most
	// two pages are kept in memory.
	if want := []string{"", "c1", "c2", "c1"}; !slices.Equal(cursors, want) {
		t.Errorf("cursors %q, want %q", cursors, want)
	}
	for _, fr := range srv.Frames() {
		if fr.Event == "phx_join" && string(fr.Payload) != `{"level":"warning"}` {
			t.Errorf("join payload %s, want no from", fr.Payload)
		}
	}
}

func TestGapFillWhenNothingShownStartsAMinuteBeforeTheLastRead(t *testing.T) {
	noLeaks(t)
	srv := channeltest.New(t, channeltest.Options{})
	f := pages(page{})
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	r := start(t, srv.URL, f, func(o *follow.Options) { o.Now = func() time.Time { return now } })
	waitJoins(t, srv, 1)
	eventually(t, "the backlog", func() bool { return len(f.Calls()) == 1 })
	srv.Drop()
	waitJoins(t, srv, 2)
	eventually(t, "the gap fill", func() bool { return len(f.Calls()) == 2 })
	if got := f.Calls()[1]["from"]; got != "2026-11-11T11:59:00.000Z" {
		t.Errorf("gap fill from %q", got)
	}
	_ = r
}

func TestPrintErrorEnds(t *testing.T) {
	noLeaks(t)
	srv := channeltest.New(t, channeltest.Options{})
	broken := errors.New("broken pipe")
	r := start(t, srv.URL, pages(page{records: recs(rec("a", 1))}), func(o *follow.Options) {
		o.Print = func([]json.RawMessage) error { return broken }
	})
	if err := r.result(); !errors.Is(err, broken) {
		t.Errorf("Run = %v, want the print error", err)
	}
}

func TestRecordsWithoutIDAreLeftOut(t *testing.T) {
	noLeaks(t)
	srv := channeltest.New(t, channeltest.Options{})
	r := start(t, srv.URL, pages(page{}), nil)
	waitJoins(t, srv, 1)
	mustPush(t, srv, push(0, json.RawMessage(`{"at":"2026-11-11T11:00:00Z"}`), json.RawMessage(`"x"`), rec("a", 1)))
	printedIs(t, r, "a")
}

func TestMissingOptions(t *testing.T) {
	if err := follow.Run(t.Context(), follow.Options{}); err == nil {
		t.Error("Run without options succeeded")
	}
}
