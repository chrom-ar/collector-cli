//go:build live

package main

import (
	"os"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
)

// TestLive drives collector against a real Collector, never in the
// default run:
//
//	COLLECTOR_LIVE_URL=http://localhost:4001 COLLECTOR_LIVE_TOKEN=cltp_… \
//	COLLECTOR_LIVE_ORG=acme COLLECTOR_LIVE_PROJECT=shop \
//	  go test -tags live -run TestLive ./cmd/collector
//
// Use a local Collector with seeded data, never app.clt.chrom.ar. Only
// reads, plus a dry-run upload: nothing in Collector changes.
func TestLive(t *testing.T) {
	url, token := os.Getenv("COLLECTOR_LIVE_URL"), os.Getenv("COLLECTOR_LIVE_TOKEN")
	org, project := os.Getenv("COLLECTOR_LIVE_ORG"), os.Getenv("COLLECTOR_LIVE_PROJECT")
	if url == "" || token == "" || org == "" || project == "" {
		t.Skip("set COLLECTOR_LIVE_URL, COLLECTOR_LIVE_TOKEN, COLLECTOR_LIVE_ORG and COLLECTOR_LIVE_PROJECT")
	}
	testscript.Run(t, testscript.Params{
		Dir: "testdata/live",
		Setup: func(env *testscript.Env) error {
			env.Setenv("COLLECTOR_URL", url)
			env.Setenv("COLLECTOR_TOKEN", token)
			env.Setenv("COLLECTOR_ORG", org)
			env.Setenv("COLLECTOR_PROJECT", project)
			env.Setenv("COLLECTOR_NO_UPDATE_NOTIFIER", "1")
			return nil
		},
		Cmds: map[string]func(ts *testscript.TestScript, neg bool, args []string){"exits": exits},
	})
}
