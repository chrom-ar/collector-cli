package cli

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/chrom-ar/collector-cli/internal/api/apitest"
)

// A rule turns on only once the person says yes at the prompt.
func TestThreatRulesAddAsksFirst(t *testing.T) {
	for _, tt := range []struct {
		answer string
		code   int
		posted bool
	}{
		{"y\n", ExitOK, true},
		{"\n", ExitError, false},
	} {
		t.Run(strings.TrimSpace(tt.answer), func(t *testing.T) {
			srv := apitest.NewServer(t, "threat-rules.check.ok", "threat-rules.create.ok")
			vars := map[string]string{ //nolint:gosec // A fixture's fake token.
				"COLLECTOR_URL": srv.URL, "COLLECTOR_TOKEN": "cltp_test_token_laptop",
				"COLLECTOR_ORG": "acme", "COLLECTOR_PROJECT": "shop",
			}
			var stdout, stderr bytes.Buffer
			env := Env{
				Stdout:          &stdout,
				Stderr:          &stderr,
				Stdin:           strings.NewReader(tt.answer),
				StdinIsTerminal: true,
				Getenv:          func(k string) string { return vars[k] },
				ConfigDir:       t.TempDir(),
			}
			code := Run(context.Background(), []string{"threat-rules", "add", "Two or more failed sign-ins for one account in 5 minutes"}, env)
			if code != tt.code {
				t.Fatalf("exit code = %d, want %d; stderr: %s", code, tt.code, stderr.String())
			}
			if !strings.Contains(stderr.String(), "Turn it on? [y/N] ") {
				t.Errorf("stderr = %q, want the question", stderr.String())
			}
			posted := false
			for _, r := range srv.Requests() {
				if r.Method == http.MethodPost && strings.HasSuffix(r.Path, "/threat-rules") {
					posted = true
				}
			}
			if posted != tt.posted {
				t.Errorf("turned on = %v, want %v", posted, tt.posted)
			}
			if tt.posted && !strings.Contains(stdout.String(), "On: r-Rgx7Kq2XpAb2") {
				t.Errorf("stdout = %q", stdout.String())
			}
		})
	}
}
