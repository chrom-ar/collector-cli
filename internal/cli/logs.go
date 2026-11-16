package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/chrom-ar/collector-cli/internal/api"
	"github.com/chrom-ar/collector-cli/internal/render"
	"github.com/spf13/cobra"
)

// logFilters are the flags sent under the endpoint's own names.
var logFilterNames = []string{"env", "server", "service", "request", "user", "ip", "account", "status", "path", "level", "type"}

type logsFlags struct {
	l                listFlags
	filters          map[string]*string
	since, until     string
	follow, markdown bool
}

// query is the endpoint's params from the flags and the words (q).
func (f *logsFlags) query(a *app, words []string) (api.Query, error) {
	q := api.Query{}
	if w := strings.TrimSpace(strings.Join(words, " ")); w != "" {
		q["q"] = w
	}
	now := a.env.Now()
	if f.since != "" {
		from, err := parseWhen(f.since, now, a.env.Location, "--since")
		if err != nil {
			return nil, err
		}
		q["from"] = from
	}
	if f.until != "" {
		to, err := parseWhen(f.until, now, a.env.Location, "--until")
		if err != nil {
			return nil, err
		}
		q["to"] = to
	}
	for _, name := range logFilterNames {
		if v := *f.filters[name]; v != "" {
			q[name] = v
		}
	}
	return q, nil
}

func newLogsCmd(a *app) *cobra.Command {
	f := &logsFlags{filters: map[string]*string{}}
	c := &cobra.Command{
		Use:   "logs [words...]",
		Short: "Log lines, requests and sign-ins, oldest at the top",
		Long: "What a project's servers sent: log lines, requests and sign-ins, in one\n" +
			"timeline, oldest at the top. Words search messages, paths and accounts.\n\n" +
			"One page at a time (Older: --cursor … for the next), or --all. --follow\n" +
			"streams new records live until Ctrl-C; with --json, one record per line.",
		Example: "  collector logs --since 15m\n" +
			"  collector logs --request F3xAbCdEfGh --markdown\n" +
			"  collector logs --status 5xx --env production\n" +
			"  collector logs -f --level warning",
		Args: argsBetween(0, 100),
		RunE: func(cmd *cobra.Command, args []string) error {
			if f.follow {
				switch {
				case f.markdown:
					return usageError{errors.New("--markdown can't be combined with --follow")}
				case f.until != "":
					return usageError{errors.New("--until can't be combined with --follow")}
				case f.l.cursor != "":
					return usageError{errors.New("--cursor can't be combined with --follow")}
				case f.l.all:
					return usageError{errors.New("--all can't be combined with --follow")}
				}
			}
			if f.markdown && a.flags.json {
				return usageError{errors.New("--markdown and --json don't go together")}
			}
			q, err := f.query(a, args)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			cl, org, project, err := a.project(ctx, "")
			if err != nil {
				return err
			}
			path := api.ProjectPath(org, project) + "/logs"
			if f.follow {
				return a.followLogs(ctx, cl, org, project, q, f)
			}
			pq, err := f.l.query(clone(q))
			if err != nil {
				return err
			}
			if f.markdown {
				md, err := cl.Markdown(ctx, path, pq)
				if err != nil {
					return logsError(err)
				}
				a.out("%s", render.SafeText(strings.TrimRight(md, "\n")))
				return nil
			}
			page, err := api.List[json.RawMessage](ctx, cl, path, pq, f.l.all)
			if err != nil {
				return logsError(err)
			}
			if a.flags.json {
				return a.printJSON(page.JSON(f.l.all))
			}
			if len(page.Items) == 0 {
				a.out("%s", emptyLogs(q))
				return nil
			}
			records := decodeRecords(page.Items)
			slices.Reverse(records)
			p := a.recordPrinter(*f.filters["server"] == "" && servers(records) > 1)
			for _, r := range records {
				a.out("%s", p.line(r))
			}
			if page.NextCursor != "" {
				fmt.Fprintf(a.env.Stderr, "Older: --cursor %s\n", page.NextCursor)
			}
			return nil
		},
	}
	fl := c.Flags()
	help := map[string]string{
		"env":     "only this environment",
		"server":  "only this server",
		"service": "only this OpenTelemetry service.name",
		"request": "only this request (the id on its error)",
		"user":    "only this user",
		"ip":      "requests and sign-ins from this address, and their lines",
		"account": "sign-ins of this account",
		"status":  "requests with this status: 404, 5xx…",
		"path":    "requests to this path",
		"level":   "this level or worse: warning, error…",
		"type":    "log, request, sign_in (comma separated)",
	}
	for _, name := range logFilterNames {
		f.filters[name] = fl.String(name, "", help[name])
	}
	fl.StringVar(&f.since, "since", "", "from this long ago (15m, 2h, 1d) or an ISO time")
	fl.StringVar(&f.until, "until", "", "up to this long ago, or an ISO time")
	fl.BoolVarP(&f.follow, "follow", "f", false, "stream new records live until Ctrl-C")
	fl.BoolVar(&f.markdown, "markdown", false, "Collector's text for an AI")
	f.l.add(c)
	return c
}

// logsError says "this Collector doesn't keep logs" for the 404 a
// Collector without them answers.
func logsError(err error) error {
	var e *api.Error
	if errors.As(err, &e) && e.Status == 404 {
		e.Message = "this Collector doesn't keep logs, or the project doesn't exist (" + e.Message + ")"
	}
	return err
}

func clone(q api.Query) api.Query {
	out := api.Query{}
	for k, v := range q {
		out[k] = v
	}
	return out
}

func emptyLogs(q api.Query) string {
	var named []string
	for _, k := range append([]string{"q"}, logFilterNames...) {
		if v := q[k]; v != "" {
			named = append(named, k+" "+v)
		}
	}
	if len(named) == 0 {
		return "No logs match."
	}
	return render.Safe("No logs match (" + strings.Join(named, ", ") + ").")
}

// record is one log record; only what a line shows is typed.
type record struct {
	Kind       string `json:"kind"`
	ID         string `json:"id"`
	At         string `json:"at"`
	Server     string `json:"server"`
	RequestID  string `json:"request_id"`
	Level      string `json:"level"`
	Message    string `json:"message"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	Status     any    `json:"status"`
	DurationMS any    `json:"duration_ms"`
	IP         string `json:"ip"`
	Outcome    string `json:"outcome"`
	Account    string `json:"account"`
	SignIn     string `json:"sign_in_method"`
}

func decodeRecords(raw []json.RawMessage) []record {
	out := make([]record, 0, len(raw))
	for _, r := range raw {
		var rec record
		if json.Unmarshal(r, &rec) == nil {
			out = append(out, rec)
		}
	}
	return out
}

func servers(rs []record) int {
	seen := map[string]bool{}
	for _, r := range rs {
		if r.Server != "" {
			seen[r.Server] = true
		}
	}
	return len(seen)
}

// recordPrinter formats records one per line, oldest first: the local
// time, the date only when it differs from the line above (or, for the
// first, from today), the server when more than one shows.
type recordPrinter struct {
	loc        *time.Location
	showServer bool
	lastDate   string
}

func (a *app) recordPrinter(showServer bool) *recordPrinter {
	return &recordPrinter{loc: a.env.Location, showServer: showServer, lastDate: a.env.Now().In(a.env.Location).Format("2006-01-02")}
}

func (p *recordPrinter) line(r record) string {
	var parts []string
	if at, err := time.Parse(time.RFC3339Nano, r.At); err == nil {
		at = at.In(p.loc)
		if d := at.Format("2006-01-02"); d != p.lastDate {
			parts = append(parts, d)
			p.lastDate = d
		}
		parts = append(parts, at.Format("15:04:05.000"))
	} else if r.At != "" {
		parts = append(parts, r.At)
	}
	if p.showServer && r.Server != "" {
		parts = append(parts, r.Server)
	}
	switch r.Kind {
	case "request":
		dur := ""
		if s := num(r.DurationMS); s != "" {
			dur = s + "ms"
		}
		parts = append(parts, r.Method, r.Path, num(r.Status), dur, r.IP, r.RequestID)
	case "sign_in":
		parts = append(parts, "sign_in", r.Outcome, r.Account, r.SignIn, r.IP)
	default:
		parts = append(parts, r.Level, render.Clip(r.Message, 200), r.RequestID)
	}
	nonEmpty := parts[:0]
	for _, s := range parts {
		if s != "" {
			nonEmpty = append(nonEmpty, s)
		}
	}
	return render.Safe(strings.Join(nonEmpty, " "))
}

func num(v any) string {
	switch n := v.(type) {
	case float64:
		return strconv.FormatFloat(n, 'f', -1, 64)
	case string:
		return n
	}
	return ""
}
