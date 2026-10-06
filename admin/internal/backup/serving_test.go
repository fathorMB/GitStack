package backup

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRestoreWaitServingIsLastStep(t *testing.T) {
	archive, r := tlsBackup(t)
	r.o.PublishTLS = func(context.Context) error { return nil }
	r.o.WaitServing = func(context.Context) error { r.f.note("serving"); return nil }
	if err := Restore(context.Background(), r.o, archive); err != nil {
		t.Fatal(err)
	}
	n := len(r.f.calls)
	if n < 3 || r.f.calls[n-3] != "restart gs-web" || r.f.calls[n-2] != "ready gs-web" || r.f.calls[n-1] != "serving" {
		t.Errorf("ordine atteso restart web → ready web → serving: %v", r.f.calls)
	}
}

func TestRestoreWaitServingWithoutPublishTLS(t *testing.T) {
	archive, r := tlsBackup(t) // insecure/letsencrypt: il web non si riavvia, l'attesa sì
	called := false
	r.o.WaitServing = func(context.Context) error { called = true; return nil }
	if err := Restore(context.Background(), r.o, archive); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("WaitServing non chiamato")
	}
}

func TestRestoreWaitServingErrorReachesCaller(t *testing.T) {
	archive, r := tlsBackup(t)
	r.o.WaitServing = func(context.Context) error { return errors.New("http://h/downloads/ca.crt: HTTP 502") }
	err := Restore(context.Background(), r.o, archive)
	var se *ServingError
	if !errors.As(err, &se) {
		t.Fatalf("atteso ServingError, ottenuto %v", err)
	}
	if !strings.Contains(err.Error(), "HTTP 502") || !strings.Contains(err.Error(), "gitstack status") {
		t.Errorf("messaggio: %v", err)
	}
	var ref *RefusedError
	if errors.As(err, &ref) {
		t.Error("non è un rifiuto")
	}
}
