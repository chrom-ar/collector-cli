package cli

import (
	"errors"
	"net/url"
	"strings"

	"github.com/chrom-ar/collector-cli/internal/api"
	"github.com/spf13/cobra"
)

func newOpenCmd(a *app) *cobra.Command {
	var noBrowser bool
	f := &logsFlags{filters: map[string]*string{}}
	c := &cobra.Command{
		Use:   "open [issue <id|url> | logs [words...] | speed | settings | setup]",
		Short: "Open the web app's page for a project, issue or log query",
		Long: "Open a page of Collector's web app: the project's issues (no argument),\n" +
			"one issue, the log explorer with the same filters as collector logs, the\n" +
			"Speed tab, the project's settings, or its setup page (the DSN and how\n" +
			"to install an SDK). The URL is printed too.",
		Example: "  collector open\n" +
			"  collector open issue 2C7ZffP787kc\n" +
			"  collector open logs --request F3xAbCdEfGh\n" +
			"  collector open settings --no-browser\n" +
			"  collector open setup",
		Args: argsBetween(0, 100),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			page := ""
			if len(args) > 0 {
				page, args = args[0], args[1:]
			}
			var link string
			switch page {
			case "issue":
				if len(args) != 1 {
					return usageError{errors.New("missing arguments: collector open issue <id|url>")}
				}
				cl, _, err := a.client()
				if err != nil {
					return err
				}
				path, err := a.locate(ctx, cl, args[0])
				if err != nil {
					return err
				}
				// /orgs/o/projects/p/issues/id → /o/o/p/p/issues/id
				parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
				link = cl.Origin() + "/o/" + parts[1] + "/p/" + parts[3] + "/issues/" + parts[5]
			case "", "logs", "speed", "settings", "setup":
				if page != "logs" && len(args) > 0 {
					return usageError{errors.New("unexpected arguments: " + strings.Join(args, " "))}
				}
				cl, org, project, err := a.project(ctx, "")
				if err != nil {
					return err
				}
				link = cl.Origin() + "/o/" + api.Seg(org) + "/p/" + api.Seg(project)
				if page != "" {
					link += "/" + page
				}
				if page == "logs" {
					q, err := f.query(a, args)
					if err != nil {
						return err
					}
					v := url.Values{}
					for k, val := range q {
						v.Set(k, val)
					}
					if len(v) > 0 {
						link += "?" + v.Encode()
					}
				}
			default:
				return usageError{errors.New("unknown page " + quote(page) + ": use issue, logs, speed, settings or setup")}
			}
			a.out("%s", link)
			if a.env.StdoutIsTerminal && !noBrowser {
				_ = a.env.OpenBrowser(link)
			}
			return nil
		},
	}
	fl := c.Flags()
	fl.BoolVar(&noBrowser, "no-browser", false, "only print the URL")
	for _, name := range logFilterNames {
		f.filters[name] = fl.String(name, "", "logs: only records with this "+name)
	}
	fl.StringVar(&f.since, "since", "", "logs: from this long ago (15m, 2h, 1d) or an ISO time")
	fl.StringVar(&f.until, "until", "", "logs: up to this long ago, or an ISO time")
	return c
}

func quote(s string) string { return `"` + s + `"` }
