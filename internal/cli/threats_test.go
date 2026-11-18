package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
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
			if !strings.Contains(stderr.String(), "Turn it on? [y/N, or type a change] ") {
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

// Words typed at the prompt go back to Collector with the last check's
// token, and what turns on is the newest version.
func TestThreatRulesAddTakesChangesAtThePrompt(t *testing.T) {
	var bodies []map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/threat-rules/check") && body["token"] == "":
			_, _ = io.WriteString(w, `{"status":"ok","text":"2 or more.","sentence":"At 2 or more.","preview":null,"messages":1,"token":"t1"}`)
		case strings.HasSuffix(r.URL.Path, "/threat-rules/check"):
			_, _ = io.WriteString(w, `{"status":"ok","text":"30 or more.","sentence":"At 30 or more.","preview":null,"messages":2,"token":"t2"}`)
		default:
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"ref":"r-1","name":"Thirty"}`)
		}
	}))
	t.Cleanup(srv.Close)
	vars := map[string]string{ //nolint:gosec // A fake token.
		"COLLECTOR_URL": srv.URL, "COLLECTOR_TOKEN": "cltp_test_token_laptop",
		"COLLECTOR_ORG": "acme", "COLLECTOR_PROJECT": "shop",
	}
	var stdout, stderr bytes.Buffer
	env := Env{
		Stdout: &stdout, Stderr: &stderr,
		Stdin:           strings.NewReader("Make it 30\ny\n"),
		StdinIsTerminal: true,
		Getenv:          func(k string) string { return vars[k] },
		ConfigDir:       t.TempDir(),
	}
	if code := Run(context.Background(), []string{"threat-rules", "add", "Two or more"}, env); code != ExitOK {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}
	want := []map[string]string{{"text": "Two or more"}, {"text": "Make it 30", "token": "t1"}, {"token": "t2"}}
	if len(bodies) != len(want) {
		t.Fatalf("requests = %v, want %v", bodies, want)
	}
	for i := range want {
		if !maps.Equal(bodies[i], want[i]) {
			t.Errorf("request %d = %v, want %v", i, bodies[i], want[i])
		}
	}
	if !strings.Contains(stdout.String(), "30 or more.") || !strings.HasSuffix(stdout.String(), "On: r-1 Thirty\n") {
		t.Errorf("stdout = %q", stdout.String())
	}
}
