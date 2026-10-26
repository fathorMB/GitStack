package httpserver

import (
	"strings"
	"testing"
	"time"
)

// GIT-350: `since` accetta una durata (5m, 1h, 7d) o un istante RFC 3339; il resto è 400.
func TestParseAppLogSince(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	ok := map[string]time.Time{
		"5m":                   now.Add(-5 * time.Minute),
		"1h":                   now.Add(-time.Hour),
		"7d":                   now.Add(-7 * 24 * time.Hour),
		"2026-10-26T10:00:00Z": time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC),
	}
	for in, want := range ok {
		got, valid := parseAppLogSince(in, now)
		if !valid || !got.Equal(want) {
			t.Errorf("parseAppLogSince(%q) = %v, %v; voluto %v", in, got, valid, want)
		}
	}
	for _, in := range []string{"", "5", "h", "5w", "-5m", "ieri", "99999999999999999999d", strings.Repeat("9", 40) + "m", "3650001d"} {
		if got, valid := parseAppLogSince(in, now); valid {
			t.Errorf("parseAppLogSince(%q) accettato: %v", in, got)
		}
	}
}
