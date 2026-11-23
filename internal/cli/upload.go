package cli

import (
	"errors"
	"fmt"
	"slices"

	"github.com/chrom-ar/collector-cli/internal/render"
	"github.com/chrom-ar/collector-cli/internal/sourcemaps"
	"github.com/spf13/cobra"
)

// failure is an error that exits 1 whatever it is: upload and test use
// only 0, 1 and 2.
type failure struct{ err error }

func (e failure) Error() string { return e.err.Error() }
func (e failure) Unwrap() error { return e.err }

// onlyUsageOrFailure maps err to exit 2 (usage) or 1 (anything else).
func onlyUsageOrFailure(err error) error {
	if err == nil {
		return nil
	}
	var se silentError
	if errors.As(err, &se) {
		return err
	}
	if code := exitCode(err); code == ExitUsage {
		return err
	}
	return failure{err}
}

func newUploadCmd(a *app) *cobra.Command {
	var (
		release, dsn, urlPrefix string
		del, dryRun, sources    bool
	)
	c := &cobra.Command{
		Use:   "upload <dir>",
		Short: "Upload a build's source maps",
		Long: "Upload the source maps under <dir>, so Collector shows your own code in\n" +
			"stack traces. Upload before you deploy.\n\n" +
			"--sources is for apps that serve their JavaScript as written, with no\n" +
			"bundler (import maps, say): each script without a map gets one made\n" +
			"line for line, carrying the script itself. Minified scripts are skipped\n" +
			"with a warning, and --delete leaves these alone.\n\n" +
			"Credentials: COLLECTOR_UPLOAD_TOKEN, read only from the environment\n" +
			"(create one with collector upload-tokens create). Without it, a personal\n" +
			"token of an owner or admin (collector login, or COLLECTOR_TOKEN) with\n" +
			"--org and --project.\n\n" +
			"Exit codes: 0 everything uploaded, 1 something failed, 2 wrong usage.",
		Example: "  COLLECTOR_UPLOAD_TOKEN=cltu_… collector upload ./dist --release \"$GIT_SHA\" --dsn \"$COLLECTOR_DSN\"\n" +
			"  collector upload ./dist --release 1.4.2 --url-prefix /assets --delete\n" +
			"  collector upload .next --release 1.4.2 --dry-run\n" +
			"  collector upload public/assets --url-prefix /assets --sources --release \"$GIT_SHA\"",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return usageError{errors.New("missing the directory to upload, e.g. collector upload ./dist")}
			}
			return argsBetween(1, 1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return onlyUsageOrFailure(a.upload(cmd, args[0], release, dsn, urlPrefix, del, dryRun, sources))
		},
	}
	f := c.Flags()
	f.StringVar(&release, "release", "", "the release your app reports, exactly as passed to init({ release }) (or $COLLECTOR_RELEASE)")
	f.StringVar(&dsn, "dsn", "", "your project's DSN, to find Collector (or $COLLECTOR_DSN)")
	f.StringVar(&urlPrefix, "url-prefix", "/", "the URL path <dir> is served at")
	f.BoolVar(&del, "delete", false, "delete the .map files after a successful upload")
	f.BoolVar(&dryRun, "dry-run", false, "show what would be uploaded, without sending anything")
	f.BoolVar(&sources, "sources", false, "make maps for scripts served as written, with none of their own")
	return c
}

func (a *app) upload(cmd *cobra.Command, dir, release, dsn, urlPrefix string, del, dryRun, sources bool) error {
	ctx := cmd.Context()
	release = trimmed(cmpOr(release, a.env.Getenv("COLLECTOR_RELEASE")))
	if release == "" {
		return usageError{errors.New("missing --release (or COLLECTOR_RELEASE). Use the same value your app passes to init({ release })")}
	}
	dsn = trimmed(cmpOr(dsn, a.env.Getenv("COLLECTOR_DSN")))
	host, err := sourcemaps.HostFromDSN(dsn)
	if err != nil {
		return usageError{err}
	}
	// CI secrets are often pasted with a trailing newline.
	token := sourcemaps.Token(trimmed(a.env.Getenv("COLLECTOR_UPLOAD_TOKEN")))
	personal := token == "" && !dryRun
	if personal {
		// With no --url, COLLECTOR_URL or login, the DSN (when given)
		// says which Collector this is.
		if cfg, _, err := a.config(); err == nil && a.flags.url == "" && a.env.Getenv("COLLECTOR_URL") == "" && cfg.URL == "" && dsn != "" {
			a.flags.url = host
		}
		s, err := a.resolve()
		if err != nil {
			return err
		}
		if s.token == "" {
			return usageError{errors.New("missing COLLECTOR_UPLOAD_TOKEN. Create an upload token in your project's settings, or sign in with: collector login")}
		}
		token, host = sourcemaps.Token(s.token.Reveal()), s.url
	}

	found, err := sourcemaps.Find(dir, sourcemaps.FindOptions{URLPrefix: urlPrefix, Sources: sources})
	if err != nil {
		return err
	}
	for _, w := range found.Warnings {
		a.warn("%s", render.Safe(w))
	}
	if len(found.Maps) == 0 && sources {
		return fmt.Errorf("nothing to upload from %s: no source maps, and no scripts to make one for", dir)
	}
	if len(found.Maps) == 0 {
		return fmt.Errorf("no source maps found in %s. Turn them on in your bundler (e.g. build.sourcemap in Vite) and build again; for scripts served as written, pass --sources", dir)
	}
	if dryRun {
		for _, m := range found.Maps {
			made := ""
			if m.Generated {
				made = " (map made from the script)"
			}
			a.out("would upload %s%s", render.Safe(m.Path), made)
		}
		a.out("%d map(s) for release %s to %s (dry run: nothing sent)", len(found.Maps), render.Safe(release), host)
		return nil
	}

	project := ""
	if personal {
		_, org, slug, err := a.project(ctx, "")
		if err != nil {
			return err
		}
		project = org + "/" + slug
	}
	res := sourcemaps.Upload(ctx, found.Maps, sourcemaps.Options{
		Release:    release,
		Host:       host,
		Token:      token,
		Project:    project,
		HTTPClient: a.env.HTTPClient,
		Log:        func(line string) { a.out("%s", render.Safe(line)) },
		Sleep:      a.env.Sleep,
		UserAgent:  a.userAgent(),
	})
	for _, e := range res.Errors {
		fmt.Fprintf(a.env.Stderr, "error: %s\n", render.Safe(e))
	}
	if !res.OK {
		fmt.Fprintf(a.env.Stderr, "Uploaded %d of %d map(s) for release %s.\n", len(res.Uploaded), len(found.Maps), render.Safe(release))
		return silentError{ExitError}
	}
	a.out("Uploaded %d map(s) for release %s.", len(found.Maps), render.Safe(release))
	if del && slices.ContainsFunc(found.Maps, func(m sourcemaps.Map) bool { return !m.Generated }) {
		if err := sourcemaps.Delete(found.Maps); err != nil {
			return fmt.Errorf("uploaded, but couldn't delete the .map files: %w", err)
		}
		a.out("Deleted the .map files.")
	}
	return nil
}

func newTestCmd(a *app) *cobra.Command {
	var dsn string
	c := &cobra.Command{
		Use:   "test",
		Short: "Send one test event and show Collector's answer",
		Long: "Send one test event, the way a server SDK does, to check that the DSN\n" +
			"works. COLLECTOR_ENVIRONMENT and COLLECTOR_RELEASE go with it when set.\n\n" +
			"Exit codes: 0 accepted, 1 refused or unreachable, 2 wrong usage.",
		Example: "  collector test --dsn \"$(collector dsn)\"\n  COLLECTOR_DSN=https://clt_…@app.clt.chrom.ar collector test",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			host, err := a.env.Hostname()
			if err != nil {
				host = ""
			}
			res, err := sourcemaps.SendTestEvent(cmd.Context(), sourcemaps.TestEventOptions{
				DSN:         trimmed(cmpOr(dsn, a.env.Getenv("COLLECTOR_DSN"))),
				Environment: a.env.Getenv("COLLECTOR_ENVIRONMENT"),
				Release:     a.env.Getenv("COLLECTOR_RELEASE"),
				Version:     a.env.Build.Version,
				Hostname:    host,
				HTTPClient:  a.env.HTTPClient,
				UserAgent:   a.userAgent(),
			})
			if errors.Is(err, sourcemaps.ErrUsage) {
				return usageError{err}
			}
			if err != nil {
				return failure{err}
			}
			a.out("%s", res.String())
			return nil
		},
	}
	c.Flags().StringVar(&dsn, "dsn", "", "your project's DSN (or $COLLECTOR_DSN)")
	return c
}
