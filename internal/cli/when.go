package cli

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"time"
)

var relative = regexp.MustCompile(`^(\d+)([smhd])$`)

// isoLayouts are the absolute times --since and --until take.
var isoLayouts = []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"}

// parseWhen reads --since/--until: 15m, 2h, 1d (that long before now),
// or an ISO 8601 time or date (local time when it has no zone). Anything
// else, an impossible date (2026-02-30) or an absurd span, is a usage
// error naming the flag. It returns the time as ISO 8601 UTC.
func parseWhen(s string, now time.Time, loc *time.Location, flag string) (string, error) {
	bad := func(why string) error {
		return usageError{fmt.Errorf("%s %q: %s; use 15m, 2h, 1d or an ISO time like 2026-11-13T09:00:00Z", flag, s, why)}
	}
	if m := relative.FindStringSubmatch(s); m != nil {
		n, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil || n < 1 {
			return "", bad("not a span")
		}
		unit := map[string]time.Duration{"s": time.Second, "m": time.Minute, "h": time.Hour, "d": 24 * time.Hour}[m[2]]
		if n > math.MaxInt64/int64(unit) || n*int64(unit) > int64(100*365*24*time.Hour) {
			return "", bad("too long ago")
		}
		return iso(now.Add(-time.Duration(n) * unit)), nil
	}
	for _, layout := range isoLayouts {
		var (
			t   time.Time
			err error
		)
		if layout == time.RFC3339Nano {
			t, err = time.Parse(layout, s)
		} else {
			t, err = time.ParseInLocation(layout, s, loc)
		}
		if err == nil {
			return iso(t), nil
		}
	}
	return "", bad("not a time")
}

func iso(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z07:00") }
