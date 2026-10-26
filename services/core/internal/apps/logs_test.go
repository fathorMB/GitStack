package apps

import (
	"strings"
	"testing"
	"time"
)

// GIT-350: ogni riga di Kubernetes ha in testa il timestamp RFC 3339 con decimali; il testo è il resto.
func TestParseLogLine(t *testing.T) {
	fallback := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	at, text := parseLogLine("2026-10-26T10:15:30.123456789Z avvio del servizio", fallback)
	if want := time.Date(2026, 10, 26, 10, 15, 30, 123456000, time.UTC); !at.Equal(want) {
		t.Errorf("orario = %v, voluto %v (al microsecondo)", at, want)
	}
	if text != "avvio del servizio" {
		t.Errorf("testo = %q", text)
	}
	// Senza timestamp: l'orario è quello della raccolta e la riga è intera.
	at, text = parseLogLine("riga senza orario", fallback)
	if !at.Equal(fallback) || text != "riga senza orario" {
		t.Errorf("riga senza orario: %v %q", at, text)
	}
	at, text = parseLogLine("non-una-data riga", fallback)
	if !at.Equal(fallback) || text != "non-una-data riga" {
		t.Errorf("timestamp non valido: %v %q", at, text)
	}
}

// Una riga oltre il limite si taglia senza spezzare un carattere UTF-8, con il segno di troncamento.
func TestCapLine(t *testing.T) {
	if got := capLine("breve"); got != "breve" {
		t.Errorf("capLine(breve) = %q", got)
	}
	// Un byte ASCII davanti sposta il limite a metà di una «è» (2 byte): il taglio non deve spezzarla.
	long := "a" + strings.Repeat("è", logLineMaxBytes)
	got := capLine(long)
	if !strings.HasSuffix(got, logTruncatedMarker) {
		t.Fatalf("manca il segno di troncamento")
	}
	body := strings.TrimSuffix(got, logTruncatedMarker)
	if len(body) > logLineMaxBytes || !strings.HasPrefix(long, body) {
		t.Errorf("troncamento non valido: %d byte", len(body))
	}
	if strings.ContainsRune(body, '�') {
		t.Errorf("il troncamento ha spezzato un carattere")
	}
}
