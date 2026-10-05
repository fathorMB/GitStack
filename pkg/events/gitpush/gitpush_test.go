package gitpush_test

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/pkg/events"
	"github.com/fathorMB/GitStack/pkg/events/gitpush"
)

func TestRegistroDecodificaV1(t *testing.T) {
	raw, err := os.ReadFile("testdata/example_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	reg := events.NewRegistry()
	gitpush.Register(reg)
	var env events.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	got, err := reg.Decode(env)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	p, ok := got.(gitpush.Payload)
	if !ok {
		t.Fatalf("tipo %T", got)
	}
	if p.Repo.DefaultBranch != "main" || p.Pusher.Type != gitpush.PusherAgent || len(p.Refs) != 2 {
		t.Fatalf("payload inatteso: %+v", p)
	}
	if !p.Refs[0].IsDefaultBranch || p.Refs[0].Commits[0].Author.Email != "ada@example.com" {
		t.Fatalf("ref inatteso: %+v", p.Refs[0])
	}
	if p.Refs[1].Before != gitpush.ZeroSHA {
		t.Fatalf("la creazione deve avere before a zero: %+v", p.Refs[1])
	}
}

func TestVersioneSconosciuta(t *testing.T) {
	reg := events.NewRegistry()
	gitpush.Register(reg)
	_, err := reg.Decode(events.Envelope{Name: gitpush.Name, Version: 2, Payload: []byte(`{}`)})
	var ue *events.UnknownSchemaError
	if !errors.As(err, &ue) {
		t.Fatalf("atteso UnknownSchemaError, ottenuto %v", err)
	}
}

func TestDecodeRifiutaPayloadNonValido(t *testing.T) {
	for name, raw := range map[string]string{
		"non json":    `{`,
		"senza repo":  `{"pusher":{"id":"u"},"refs":[]}`,
		"senza push.": `{"repo":{"id":"r"},"refs":[]}`,
	} {
		if _, err := gitpush.Decode([]byte(raw)); err == nil {
			t.Errorf("%s: atteso errore", name)
		}
	}
}

// L'esempio in docs/events.md è lo stesso file che i test decodificano: se
// uno cambia senza l'altro, il test si rompe.
func TestEsempioInDocsEvents(t *testing.T) {
	ex, err := os.ReadFile("testdata/example_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile("../../../docs/events.md")
	if err != nil {
		t.Fatal(err)
	}
	norm := func(b []byte) string { return strings.ReplaceAll(string(b), "\r\n", "\n") }
	if !strings.Contains(norm(doc), strings.TrimSpace(norm(ex))) {
		t.Fatal("docs/events.md non contiene l'esempio di testdata/example_v1.json")
	}
}
