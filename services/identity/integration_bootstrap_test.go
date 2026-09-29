//go:build integration

package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/dbtest"
	"github.com/fathorMB/GitStack/services/identity/internal/users"
)

// Con un database vero: bootstrap, secondo avvio (no-op) e password mai nei
// log del servizio.
func TestBootstrapAdminRealDBNoPasswordInLogs(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	svc := users.New(pool, time.Now)
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg := bootstrapCfg()

	for i := 0; i < 2; i++ { // primo avvio e riavvio
		if err := bootstrapAdmin(context.Background(), cfg, svc, logger); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Contains(buf.String(), secretPw) {
		t.Fatalf("password nel log:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "utente admin creato") || !strings.Contains(buf.String(), "non necessario") {
		t.Fatalf("log inattesi:\n%s", buf.String())
	}
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM identity.users WHERE is_admin`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("admin=%d err=%v", n, err)
	}
}
