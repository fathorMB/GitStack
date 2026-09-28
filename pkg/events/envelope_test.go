package events_test

import (
	"testing"
	"time"

	"github.com/fathorMB/GitStack/pkg/events"
)

func TestNewEnvelope_SetsIDTimeAndPayload(t *testing.T) {
	before := time.Now().UTC()
	env, err := events.NewEnvelope("demo.greet", 1, greetPayload{Message: "ciao"})
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}
	after := time.Now().UTC()

	if env.Name != "demo.greet" || env.Version != 1 {
		t.Fatalf("nome/versione = %q v%d, voluti %q v%d", env.Name, env.Version, "demo.greet", 1)
	}
	if env.ID == "" {
		t.Fatal("ID non deve essere vuoto")
	}
	if env.Time.Before(before) || env.Time.After(after) {
		t.Fatalf("Time = %v, atteso tra %v e %v", env.Time, before, after)
	}

	data, err := env.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	roundtripped, err := events.UnmarshalEnvelope(data)
	if err != nil {
		t.Fatalf("UnmarshalEnvelope: %v", err)
	}
	if roundtripped.Name != env.Name || roundtripped.Version != env.Version || roundtripped.ID != env.ID {
		t.Fatalf("roundtrip = %+v, voluto %+v", roundtripped, env)
	}
}

func TestNewEnvelope_EmptyNameFails(t *testing.T) {
	if _, err := events.NewEnvelope("", 1, greetPayload{}); err == nil {
		t.Fatal("nome evento vuoto deve fallire")
	}
}
