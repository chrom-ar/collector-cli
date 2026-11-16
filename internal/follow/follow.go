// Package follow is the logic of `collector logs --follow` (SPEC 4.3): the
// backlog over REST, then live records over the logs channel, reconnecting
// and filling the gap when the stream drops, until the context ends. It
// prints nothing itself: records, skipped counts and reconnects go to the
// caller's functions.
package follow

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/chrom-ar/collector-cli/internal/api"
	"github.com/chrom-ar/collector-cli/internal/channel"
)

const (
	// seenMax is how many record ids are remembered to drop duplicates.
	seenMax = 10_000
	// backoffMax caps the wait between reconnects.
	backoffMax = 30 * time.Second
	// gapMargin: the gap fill starts this long before the newest record
	// shown, because servers' clocks and batches lag.
	gapMargin = time.Minute
	// maxFirstAttempts is how many times to try a stream that never opened.
	maxFirstAttempts = 5
	// pageSize is the page size when reading every page.
	pageSize = "100"
	// fromLayout is how the gap fill's from is written.
	fromLayout = "2006-01-02T15:04:05.000Z07:00"
)

// ErrNeverOpened matches the error Run returns when the live stream never
// opened after 5 attempts.
var ErrNeverOpened = errors.New("this Collector may not support --follow yet")

// NeverOpenedError is the error Run returns when the live stream never
// opened after 5 attempts. Err is the last attempt's error.
type NeverOpenedError struct {
	URL string
	Err error
}

func (e *NeverOpenedError) Error() string {
	return "couldn't open the live stream at " + e.URL + "; this Collector may not support --follow yet"
}

// Is makes a NeverOpenedError match ErrNeverOpened.
func (e *NeverOpenedError) Is(target error) bool { return target == ErrNeverOpened }

// Unwrap returns the last attempt's error.
func (e *NeverOpenedError) Unwrap() error { return e.Err }

// Fetcher reads a project's records over REST (GET …/logs): one page,
// newest first, and the cursor of the next ("" on the last). It must return
// when ctx ends. Its errors are *api.Error for Collector's answers and
// network failures.
type Fetcher interface {
	Logs(ctx context.Context, q api.Query) (records []json.RawMessage, nextCursor string, err error)
}

// Options configure Run. Fetch, Dial, Topic and Print are required.
type Options struct {
	// Fetch reads records over REST.
	Fetch Fetcher
	// Dial opens the socket, passing onRecords as channel.Options.OnRecords.
	Dial func(ctx context.Context, onRecords func(channel.Records)) (*channel.Conn, error)
	// URL names the socket in the error when it never opens.
	URL string
	// Topic is the channel, logs:<org>/<project>.
	Topic string
	// Filters are the REST query's filters, from included (--since): with
	// from, the backlog is every page from it; without, one page. The join
	// payload is these minus from and to.
	Filters map[string]string
	// Limit is the backlog's page size; "" leaves Collector's default for
	// one page, and 100 for every page.
	Limit string
	// Print prints records, oldest first, never one twice. An error ends Run
	// with it.
	Print func(records []json.RawMessage) error
	// Skipped, when set, is told how many records Collector left out of a
	// push (too many to stream), before that push's records.
	Skipped func(n int)
	// Reconnecting, when set, is told the stream dropped or couldn't open,
	// why, and how long until the next try.
	Reconnecting func(in time.Duration, cause error)
	// Sleep waits between tries; it must return ctx's error when ctx ends.
	// nil is api.Sleep.
	Sleep func(ctx context.Context, d time.Duration) error
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// Run follows the logs until ctx ends, and then returns ctx's error.
// Otherwise it returns:
//   - an error matching channel.ErrRevoked when Collector closes the stream
//     (4001/4003) or refuses the join as forbidden;
//   - the *channel.JoinError for any other refused join (not_found,
//     invalid, unknown codes), except unavailable, which is retried;
//   - the *api.Error of a REST answer that isn't worth retrying (401, 404 …):
//     after a failed connect, REST is asked whether the token still works;
//   - a *NeverOpenedError after 5 failed attempts, if the stream never
//     opened; once it has, it is retried forever;
//   - Print's error.
//
// Network errors, 5xx and 429 from REST, and dropped connections are
// retried with a backoff of 1, 2, 4 … 30 s.
func Run(ctx context.Context, o Options) error {
	if o.Fetch == nil || o.Dial == nil || o.Print == nil || o.Topic == "" {
		return errors.New("follow: Fetch, Dial, Topic and Print are required")
	}
	if o.Sleep == nil {
		o.Sleep = api.Sleep
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	live := api.Query{}
	for k, v := range o.Filters {
		if k != "from" && k != "to" {
			live[k] = v
		}
	}
	f := &follower{o: o, live: live, seen: newSeen(seenMax)}
	return f.run(ctx)
}

type follower struct {
	o      Options
	live   api.Query // the filters minus from and to
	seen   *seen
	newest time.Time // the newest record shown
	synced time.Time // when the last backlog read started
	opened bool      // whether the stream ever opened
}

func (f *follower) run(ctx context.Context) error {
	attempt := 0
	for {
		opened, cause, err := f.session(ctx)
		if err != nil {
			return err
		}
		if opened {
			attempt = 0
		}
		if !f.opened && attempt+1 >= maxFirstAttempts {
			return &NeverOpenedError{URL: f.o.URL, Err: cause}
		}
		wait := min(time.Second<<attempt, backoffMax)
		attempt++
		if f.o.Reconnecting != nil {
			f.o.Reconnecting(wait, cause)
		}
		if err := f.o.Sleep(ctx, wait); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
	}
}

// session connects, joins, reads the backlog and streams until the
// connection ends. It returns whether the stream opened, why it ended when
// trying again is worth it, or the error that ends Run.
func (f *follower) session(ctx context.Context) (opened bool, cause, err error) {
	q := newQueue()
	conn, err := f.o.Dial(ctx, q.add)
	if err != nil {
		if ctx.Err() != nil {
			return false, nil, ctx.Err()
		}
		// A refused upgrade looks like a network failure; REST says
		// whether the token still works.
		return false, err, f.probe(ctx)
	}
	defer func() { _ = conn.Close() }()

	if err := conn.Join(ctx, f.o.Topic, f.live); err != nil {
		if ctx.Err() != nil {
			return false, nil, ctx.Err()
		}
		if errors.Is(err, channel.ErrDropped) {
			return false, err, f.probe(ctx)
		}
		return false, nil, err
	}

	started := f.o.Now()
	if err := f.backlog(ctx); err != nil {
		if ctx.Err() != nil {
			return false, nil, ctx.Err()
		}
		if retryable(err) {
			return false, err, nil
		}
		return false, nil, err
	}
	f.synced = started
	f.opened = true

	// The pushes held during the backlog, then the stream.
	for {
		if err := f.drain(q); err != nil {
			return true, nil, err
		}
		select {
		case <-ctx.Done():
			return true, nil, ctx.Err()
		case <-q.ready:
		case <-conn.Done():
			if err := f.drain(q); err != nil {
				return true, nil, err
			}
			err := conn.Err()
			if ctx.Err() != nil {
				return true, nil, ctx.Err()
			}
			if errors.Is(err, channel.ErrRevoked) {
				return true, nil, err
			}
			return true, err, nil
		}
	}
}

// probe asks REST whether the token still works: nil unless the answer
// ends Run.
func (f *follower) probe(ctx context.Context) error {
	q := maps.Clone(f.live)
	q["limit"] = "1"
	_, _, err := f.o.Fetch.Logs(ctx, q)
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case err == nil || retryable(err):
		return nil
	}
	return err
}

// backlog prints what the stream can't send: the first time, one page, or
// every page from --since; after a drop, every page since a minute before
// the newest record shown (or the last read, when none was).
func (f *follower) backlog(ctx context.Context) error {
	if !f.opened {
		q := api.Query(maps.Clone(f.o.Filters))
		if q == nil {
			q = api.Query{}
		}
		delete(q, "to")
		if q["from"] == "" {
			if f.o.Limit != "" {
				q["limit"] = f.o.Limit
			}
			records, _, err := f.o.Fetch.Logs(ctx, q)
			if err != nil {
				return err
			}
			return f.print(oldestFirst(records))
		}
		q["limit"] = pageSize
		if f.o.Limit != "" {
			q["limit"] = f.o.Limit
		}
		return f.every(ctx, q)
	}
	from := f.newest
	if from.IsZero() {
		from = f.synced
	}
	q := maps.Clone(f.live)
	q["from"] = from.Add(-gapMargin).UTC().Format(fromLayout)
	q["limit"] = pageSize
	return f.every(ctx, q)
}

// every prints every page of q, oldest first, holding at most two pages:
// it walks the pages newest first keeping only their cursors, then reads
// them back from the oldest.
func (f *follower) every(ctx context.Context, q api.Query) error {
	newest, next, err := f.o.Fetch.Logs(ctx, q)
	if err != nil {
		return err
	}
	var cursors []string
	var oldest []json.RawMessage
	for next != "" {
		cursors = append(cursors, next)
		if oldest, next, err = f.fetch(ctx, q, next); err != nil {
			return err
		}
	}
	if len(cursors) > 0 {
		if err := f.print(oldestFirst(oldest)); err != nil {
			return err
		}
		for _, cursor := range slices.Backward(cursors[:len(cursors)-1]) {
			records, _, err := f.fetch(ctx, q, cursor)
			if err != nil {
				return err
			}
			if err := f.print(oldestFirst(records)); err != nil {
				return err
			}
		}
	}
	return f.print(oldestFirst(newest))
}

func (f *follower) fetch(ctx context.Context, q api.Query, cursor string) ([]json.RawMessage, string, error) {
	q = maps.Clone(q)
	q["cursor"] = cursor
	return f.o.Fetch.Logs(ctx, q)
}

// drain prints the pushes received so far.
func (f *follower) drain(q *queue) error {
	for _, push := range q.take() {
		if push.Skipped > 0 && f.o.Skipped != nil {
			f.o.Skipped(push.Skipped)
		}
		if err := f.print(push.Data); err != nil {
			return err
		}
	}
	return nil
}

// print prints the records, oldest first, not seen before. Records without
// an id are left out: they can't be told apart.
func (f *follower) print(records []json.RawMessage) error {
	var out []json.RawMessage
	for _, raw := range records {
		var r struct {
			ID *string `json:"id"`
			At string  `json:"at"`
		}
		if json.Unmarshal(raw, &r) != nil || r.ID == nil || !f.seen.add(*r.ID) {
			continue
		}
		if at, err := time.Parse(time.RFC3339Nano, r.At); err == nil && at.After(f.newest) {
			f.newest = at
		}
		out = append(out, raw)
	}
	if len(out) == 0 {
		return nil
	}
	return f.o.Print(out)
}

func oldestFirst(newestFirst []json.RawMessage) []json.RawMessage {
	out := slices.Clone(newestFirst)
	slices.Reverse(out)
	return out
}

// retryable tells a REST error worth trying again later: a network
// failure, 429 or 5xx.
func retryable(err error) bool {
	var e *api.Error
	if !errors.As(err, &e) {
		return false
	}
	return e.Status == 0 || e.Status == http.StatusTooManyRequests || e.Status >= http.StatusInternalServerError
}

// queue holds the pushes from the channel's read goroutine until Run
// prints them, so a slow terminal never delays a heartbeat.
type queue struct {
	mu    sync.Mutex
	items []channel.Records
	ready chan struct{}
}

func newQueue() *queue { return &queue{ready: make(chan struct{}, 1)} }

func (q *queue) add(r channel.Records) {
	q.mu.Lock()
	q.items = append(q.items, r)
	q.mu.Unlock()
	select {
	case q.ready <- struct{}{}:
	default:
	}
}

func (q *queue) take() []channel.Records {
	q.mu.Lock()
	defer q.mu.Unlock()
	items := q.items
	q.items = nil
	return items
}

// seen is the ids printed, forgetting the oldest past its size.
type seen struct {
	size  int
	ids   map[string]struct{}
	order []string // oldest first
}

func newSeen(size int) *seen { return &seen{size: size, ids: map[string]struct{}{}} }

// add reports whether id is new, and remembers it.
func (s *seen) add(id string) bool {
	if _, ok := s.ids[id]; ok {
		return false
	}
	s.ids[id] = struct{}{}
	s.order = append(s.order, id)
	if len(s.order) > s.size {
		delete(s.ids, s.order[0])
		s.order = s.order[1:]
	}
	return true
}
