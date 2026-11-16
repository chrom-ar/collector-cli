package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/chrom-ar/collector-cli/internal/api"
	"github.com/chrom-ar/collector-cli/internal/channel"
	"github.com/chrom-ar/collector-cli/internal/follow"
	"github.com/chrom-ar/collector-cli/internal/render"
)

// restLogs reads logs pages for follow.
type restLogs struct {
	c    *api.Client
	path string
}

func (r restLogs) Logs(ctx context.Context, q api.Query) ([]json.RawMessage, string, error) {
	page, err := api.List[json.RawMessage](ctx, r.c, r.path, q, false)
	if err != nil {
		return nil, "", err
	}
	return page.Items, page.NextCursor, nil
}

// followLogs is logs --follow: the backlog over REST, then new records
// live, until Ctrl-C (exit 0).
func (a *app) followLogs(ctx context.Context, cl *api.Client, org, project string, q api.Query, f *logsFlags) error {
	s, err := a.resolve()
	if err != nil {
		return err
	}
	wsURL, err := channel.SocketURL(s.url)
	if err != nil {
		return usageError{err}
	}
	limit := ""
	if f.l.limit != 0 {
		if f.l.limit < 1 || f.l.limit > 100 {
			return usageError{errors.New("--limit must be 1 to 100")}
		}
		limit = fmt.Sprint(f.l.limit)
	}
	printer := a.recordPrinter(*f.filters["server"] == "")
	err = follow.Run(ctx, follow.Options{
		Fetch: restLogs{c: cl, path: api.ProjectPath(org, project) + "/logs"},
		Dial: func(ctx context.Context, onRecords func(channel.Records)) (*channel.Conn, error) {
			return channel.Dial(ctx, channel.Options{
				URL: s.url, Token: s.token.Reveal(), UserAgent: a.userAgent(),
				HTTPClient: a.env.HTTPClient, OnRecords: onRecords,
			})
		},
		URL:     wsURL,
		Topic:   "logs:" + org + "/" + project,
		Filters: q,
		Limit:   limit,
		Print: func(records []json.RawMessage) error {
			for _, raw := range records {
				if a.flags.json {
					var b bytes.Buffer
					if err := json.Compact(&b, raw); err != nil {
						continue
					}
					b.WriteByte('\n')
					if _, err := a.env.Stdout.Write(b.Bytes()); err != nil {
						return err
					}
					continue
				}
				for _, r := range decodeRecords([]json.RawMessage{raw}) {
					if _, err := fmt.Fprintln(a.env.Stdout, printer.line(r)); err != nil {
						return err
					}
				}
			}
			return nil
		},
		Skipped: func(n int) {
			fmt.Fprintf(a.env.Stderr, "… %d records skipped (too many to stream)\n", n)
		},
		Reconnecting: func(in time.Duration, _ error) {
			a.progress("connection lost; reconnecting in %s…", in)
		},
		Sleep: a.env.Sleep,
		Now:   a.env.Now,
	})
	var je *channel.JoinError
	switch {
	case err == nil, errors.Is(err, context.Canceled):
		return nil
	case errors.Is(err, follow.ErrNeverOpened):
		return err
	case errors.Is(err, channel.ErrRevoked):
		return authError{errors.New("collector closed the stream: the token was revoked or lost access")}
	case errors.As(err, &je):
		msg := render.Safe(cmpOr(je.Message, je.Code))
		if je.Code == "not_found" {
			return logsError(&api.Error{Status: 404, Code: je.Code, Message: msg})
		}
		return usageError{errors.New(msg)}
	}
	return logsError(err)
}
