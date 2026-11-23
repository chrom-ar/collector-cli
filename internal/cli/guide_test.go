package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// guideEnv is a signed-out collector pointed at a Collector serving the
// agents' guide; the last request is kept in got.
func guideEnv(t *testing.T, status int, body string, got **http.Request) (Env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = r
		w.Header().Set("Content-Type", "text/markdown")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	var stdout, stderr bytes.Buffer
	return Env{
		Stdout:     &stdout,
		Stderr:     &stderr,
		Getenv:     func(k string) string { return map[string]string{"COLLECTOR_URL": srv.URL}[k] },
		HTTPClient: srv.Client(),
		ConfigDir:  t.TempDir(),
	}, &stdout, &stderr
}

// The guide needs no sign-in, and server text can't reach the terminal
// as escape sequences.
func TestGuidePrintsTheAgentsGuide(t *testing.T) {
	var r *http.Request
	env, stdout, stderr := guideEnv(t, http.StatusOK, "# Collector for agents\n\nRun \x1b[31mcollector login\n", &r)
	if code := Run(context.Background(), []string{"guide"}, env); code != ExitOK {
		t.Fatalf("exit = %d; stderr: %s", code, stderr)
	}
	if r.URL.Path != "/agents.md" || r.URL.RawQuery != "" {
		t.Errorf("asked for %s", r.URL)
	}
	if r.Header.Get("Authorization") != "" {
		t.Error("sent credentials")
	}
	if want := "# Collector for agents\n\nRun �[31mcollector login\n"; stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

func TestGuideInSpanish(t *testing.T) {
	var r *http.Request
	env, _, stderr := guideEnv(t, http.StatusOK, "# Collector para agentes\n", &r)
	if code := Run(context.Background(), []string{"guide", "--lang", "es"}, env); code != ExitOK {
		t.Fatalf("exit = %d; stderr: %s", code, stderr)
	}
	if r.URL.Query().Get("lang") != "es" {
		t.Errorf("asked for %s", r.URL)
	}
	if code := Run(context.Background(), []string{"guide", "--lang", "fr"}, env); code != ExitUsage {
		t.Errorf("--lang fr exit = %d", code)
	}
}

// Signed out, the locale says which language; --lang beats it.
func TestGuideFollowsTheLocale(t *testing.T) {
	for _, c := range []struct{ locale, args, want string }{
		{"es_AR.UTF-8", "", "es"},
		{"en_US.UTF-8", "", "en"},
		{"C", "", ""},
		{"fr_FR.UTF-8", "", ""},
		{"es_AR.UTF-8", "en", "en"},
	} {
		var r *http.Request
		env, _, _ := guideEnv(t, http.StatusOK, "# Guide\n", &r)
		url := env.Getenv("COLLECTOR_URL")
		env.Getenv = func(k string) string {
			return map[string]string{"COLLECTOR_URL": url, "LANG": c.locale}[k]
		}
		args := []string{"guide"}
		if c.args != "" {
			args = append(args, "--lang", c.args)
		}
		if code := Run(context.Background(), args, env); code != ExitOK {
			t.Fatalf("%s: exit = %d", c.locale, code)
		}
		if got := r.URL.Query().Get("lang"); got != c.want {
			t.Errorf("LANG=%s %v: lang = %q, want %q", c.locale, args, got, c.want)
		}
	}
}

func TestGuideFails(t *testing.T) {
	var r *http.Request
	env, stdout, stderr := guideEnv(t, http.StatusNotFound, "<html>nope</html>", &r)
	if code := Run(context.Background(), []string{"guide"}, env); code != ExitError {
		t.Fatalf("exit = %d", code)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "guide") {
		t.Errorf("stdout = %q, stderr = %q", stdout, stderr)
	}
}
