package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/chrom-ar/collector-cli/internal/render"
	"github.com/spf13/cobra"
)

func newGuideCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "guide",
		Short: "Print the setup guide for AI agents, as Markdown",
		Long: "Print Collector's guide for AI coding agents: how to set a project up,\n" +
			"send errors and logs, and read what broke, as Markdown. No sign-in\n" +
			"needed. Its language: --lang en or es (or $COLLECTOR_LANG), else the\n" +
			"locale's ($LANG), else Collector's default.",
		Example: "  collector guide\n" +
			"  collector guide --lang es > COLLECTOR.md",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := a.resolve()
			if err != nil {
				return err
			}
			md, err := a.fetchGuide(cmd.Context(), s.url, cmpOr(s.lang, a.localeLang()))
			if err != nil {
				return failure{err}
			}
			a.out("%s", render.SafeText(strings.TrimRight(md, "\n")))
			return nil
		},
	}
}

// localeLang is en or es from the locale (LC_ALL, LC_MESSAGES, LANG), or
// "": nobody is signed in to say which.
func (a *app) localeLang() string {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		v := a.env.Getenv(k)
		if v == "" {
			continue
		}
		switch l, _, _ := strings.Cut(strings.ToLower(v), "_"); l {
		case "en", "es":
			return l
		}
		return ""
	}
	return ""
}

// fetchGuide gets origin/agents.md, which is public: no token is sent.
func (a *app) fetchGuide(ctx context.Context, origin, lang string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	u := origin + "/agents.md"
	if lang != "" {
		u += "?lang=" + url.QueryEscape(lang)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", fmt.Errorf("asking for the guide: %w", err)
	}
	req.Header.Set("Accept", "text/markdown")
	req.Header.Set("User-Agent", a.userAgent())
	resp, err := a.env.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("couldn't reach Collector at %s for the guide: %w", origin, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("collector at %s answered %d for the guide", origin, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", fmt.Errorf("reading the guide: %w", err)
	}
	return string(b), nil
}
