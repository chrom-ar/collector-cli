package cli

import (
	"errors"
	"testing"
	"time"
)

func TestParseWhen(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	loc := time.FixedZone("ART", -3*3600)
	good := map[string]string{
		"15m":                         "2026-11-11T11:45:00.000Z",
		"2h":                          "2026-11-11T10:00:00.000Z",
		"1d":                          "2026-11-10T12:00:00.000Z",
		"30s":                         "2026-11-11T11:59:30.000Z",
		"2026-11-11T10:00:00Z":        "2026-11-11T10:00:00.000Z",
		"2026-11-11T10:00:00.5-03:00": "2026-11-11T13:00:00.500Z",
		"2026-11-11T10:00:00":         "2026-11-11T13:00:00.000Z",
		"2026-11-11":                  "2026-11-11T03:00:00.000Z",
	}
	for in, want := range good {
		got, err := parseWhen(in, now, loc, "--since")
		if err != nil || got != want {
			t.Errorf("parseWhen(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"15x", "-5m", "yesterday", "0m", "1.5h", "99999999999d", "999999999999999999999d", "2026-02-30", "2026-13-01", "2026-11-11T25:00:00Z", ""} {
		_, err := parseWhen(in, now, loc, "--since")
		var ue usageError
		if !errors.As(err, &ue) {
			t.Errorf("parseWhen(%q) = %v, want a usage error", in, err)
		}
	}
}
