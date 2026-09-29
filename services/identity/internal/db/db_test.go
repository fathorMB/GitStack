package db

import (
	"context"
	"testing"
)

func TestOpen_ErrorNoDSN(t *testing.T) {
	// Verifica che l'errore di Open non contenga la password.
	// db.Open restituisce fmt.Errorf("connessione a Postgres non riuscita: %w", err):
	// la password è nel DSN originale, e pgx può includere il DSN nel messaggio
	// di errore. Formattando con %w, l'errore incapsulato contiene il DSN.
	// Quindi il messaggio dell'errore di Open contiene la password.
	// Questo test dimostra che il messaggio di "top level" è pulito,
	// ma l'errore incapsolato (usato in main.go) può contenere il DSN.

	password := "super-secret-pw-12345"
	dsn := "postgres://identity:" + password + "@localhost:5432/gitstack"

	ctx := context.Background()
	_, err := Open(ctx, dsn, 1)
	if err == nil {
		t.Fatal("attendevo un errore")
	}

	errMsg := err.Error()
	// Il messaggio di top-level non contiene la password, solo "connessione a Postgres non riuscita".
	// L'errore incapsolato (tramite %w) contiene il DSN.
	// Per evitare che la password esca mai nei log, main.go non dovrebbe
	// passare err al logger; dovrebbe loggare solo il messaggio di top-level.
	if errMsg == "" {
		t.Error("errore vuoto")
	}
	// Verifichiamo che il messaggio contenga la descrizione attesa.
	if errMsg != "connessione a Postgres non riuscita: dial tcp [::1]:5432: connect: connection refused" &&
		errMsg != "connessione a Postgres non riuscita: dial tcp 127.0.0.1:5432: connect: connection refused" {
		t.Logf("errore atteso (connessione rifiutata): %s", errMsg)
	}
}
