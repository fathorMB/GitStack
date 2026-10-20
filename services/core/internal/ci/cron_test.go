package ci

import (
	"io"
	"log/slog"
	"testing"
	"time"
)

func nil2() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestValidateCron_FrequenzaMinima(t *testing.T) {
	ok := []string{"*/15 * * * *", "0,30 * * * *", "0 * * * *", "0 3 * * 1-5", "30 2 1 * *", "*/20 * * * *", "0 0 * JAN SUN", "5 4 * * 7", "10,25,40,55 * * * *"}
	for _, e := range ok {
		if err := ValidateCron(e); err != nil {
			t.Errorf("%q dovrebbe essere valido: %v", e, err)
		}
	}
	bad := map[string]string{
		"* * * * *":       "ogni minuto",
		"*/5 * * * *":     "ogni 5 minuti",
		"*/14 * * * *":    "14 minuti",
		"0,10 * * * *":    "10 minuti fra due scatti",
		"0-14 * * * *":    "una raffica di 15 minuti",
		"0 0 * *":         "4 campi",
		"61 * * * *":      "minuto fuori range",
		"a b c d e":       "non numerico",
		"*/0 * * * *":     "passo zero",
		"0 24 * * *":      "ora fuori range",
		"0 0 32 * *":      "giorno fuori range",
		"5,20,34 * * * *": "14 minuti dal 20 al 34",
	}
	for e, why := range bad {
		if err := ValidateCron(e); err == nil {
			t.Errorf("%q (%s) dovrebbe essere rifiutato", e, why)
		}
	}
}

func TestCron_NextEMatches(t *testing.T) {
	c, err := ParseCron("30 2 * * 1") // lunedì alle 02:30
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC) // lunedì
	next := c.Next(from)
	want := time.Date(2026, 10, 26, 2, 30, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("Next = %v, atteso %v", next, want)
	}
	if !c.Matches(want) || c.Matches(want.Add(time.Minute)) {
		t.Error("Matches")
	}
	// Strettamente dopo: da uno scatto si passa al successivo.
	if got := c.Next(want); !got.Equal(want.AddDate(0, 0, 7)) {
		t.Errorf("successivo %v", got)
	}
	every15, _ := ParseCron("*/15 * * * *")
	if got := every15.Next(time.Date(2026, 1, 1, 10, 7, 30, 0, time.UTC)); !got.Equal(time.Date(2026, 1, 1, 10, 15, 0, 0, time.UTC)) {
		t.Errorf("*/15: %v", got)
	}
}

func TestCron_GiornoDelMeseOSettimana(t *testing.T) {
	// Con entrambi ristretti vale l'OR, come cron.
	c, _ := ParseCron("0 0 13 * 5")
	if !c.Matches(time.Date(2026, 2, 13, 0, 0, 0, 0, time.UTC)) { // venerdì 13
		t.Error("venerdì 13")
	}
	if !c.Matches(time.Date(2026, 2, 20, 0, 0, 0, 0, time.UTC)) { // venerdì 20: dow
		t.Error("un venerdì qualsiasi")
	}
	if !c.Matches(time.Date(2026, 3, 13, 0, 0, 0, 0, time.UTC)) { // 13 di venerdì? 13/3/2026 è venerdì
		t.Error("13 marzo")
	}
	if c.Matches(time.Date(2026, 2, 14, 0, 0, 0, 0, time.UTC)) {
		t.Error("sabato 14 non corrisponde")
	}
}

func TestCron_MinGap(t *testing.T) {
	c, _ := ParseCron("*/15 * * * *")
	if g := c.MinGap(); g != 15*time.Minute {
		t.Errorf("gap %v", g)
	}
	d, _ := ParseCron("0 3 * * *")
	if g := d.MinGap(); g != 24*time.Hour {
		t.Errorf("gap giornaliero %v", g)
	}
}

func TestSchedulesNelWorkflow(t *testing.T) {
	wf := mustParse(t, "on:\n  schedule:\n    - cron: '*/15 * * * *'\n    - cron: '* * * * *'\n    - cron: 'xx'\n"+jobs)
	if len(wf.Schedules) != 1 || wf.Schedules[0] != "*/15 * * * *" {
		t.Fatalf("schedule validi: %v", wf.Schedules)
	}
	if len(wf.Problems) != 2 {
		t.Errorf("attesi 2 avvisi, ottenuti %v", wf.Problems)
	}
	// Uno schedule non è un push.
	if wf.Matches(push("refs/heads/main")) || wf.Matches(Event{Kind: EventSchedule}) {
		t.Error("schedule non passa da Matches")
	}
}
