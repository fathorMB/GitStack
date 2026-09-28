package events_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/fathorMB/GitStack/pkg/events"
)

type greetPayload struct {
	Message string `json:"message"`
}

func decodeGreet(raw []byte) (any, error) {
	var p greetPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	return p, nil
}

func TestRegistry_DecodeKnownSchema(t *testing.T) {
	reg := events.NewRegistry()
	reg.Register("demo.greet", 1, decodeGreet)

	env, err := events.NewEnvelope("demo.greet", 1, greetPayload{Message: "ciao"})
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}

	got, err := reg.Decode(env)
	if err != nil {
		t.Fatalf("Decode di uno schema noto non deve fallire: %v", err)
	}
	p, ok := got.(greetPayload)
	if !ok {
		t.Fatalf("tipo decodificato = %T, voluto greetPayload", got)
	}
	if p.Message != "ciao" {
		t.Fatalf("Message = %q, voluto %q", p.Message, "ciao")
	}
}

func TestRegistry_UnknownNameDoesNotCrash(t *testing.T) {
	reg := events.NewRegistry()
	env, err := events.NewEnvelope("demo.unknown", 1, greetPayload{Message: "boh"})
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}

	_, err = reg.Decode(env)
	var unknown *events.UnknownSchemaError
	if !errors.As(err, &unknown) {
		t.Fatalf("errore = %v (%T), voluto *events.UnknownSchemaError", err, err)
	}
}

func TestRegistry_UnknownVersionDoesNotCrash(t *testing.T) {
	reg := events.NewRegistry()
	reg.Register("demo.greet", 1, decodeGreet)

	env, err := events.NewEnvelope("demo.greet", 2, greetPayload{Message: "boh"})
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}

	_, err = reg.Decode(env)
	var unknown *events.UnknownSchemaError
	if !errors.As(err, &unknown) {
		t.Fatalf("errore = %v (%T), voluto *events.UnknownSchemaError", err, err)
	}
	if unknown.Version != 2 {
		t.Fatalf("Version nell'errore = %d, voluto 2", unknown.Version)
	}
}
