package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/config"
	"github.com/fathorMB/GitStack/services/identity/internal/users"
)

type fakeBootstrapper struct {
	created bool
	err     error
	calls   int
	got     users.BootstrapAdminInput
}

func (f *fakeBootstrapper) BootstrapAdmin(_ context.Context, in users.BootstrapAdminInput) (bool, error) {
	f.calls++
	f.got = in
	return f.created, f.err
}

const secretPw = "S3cr3t-Initial-Pw-do-not-log"

func bootstrapCfg() config.Config {
	return config.Config{AdminUsername: "admin", AdminPassword: secretPw, MigrationsTimeout: time.Second}
}

func TestBootstrapAdminNeverLogsPassword(t *testing.T) {
	cases := map[string]*fakeBootstrapper{
		"creato":  {created: true},
		"esiste":  {created: false},
		"errore":  {err: errors.New("boom " + secretPw)}, // errore generico: non va loggato
		"invalid": {err: &users.ValidationError{Fields: map[string]string{"password": "troppo corta"}}},
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, nil))
			_ = bootstrapAdmin(context.Background(), bootstrapCfg(), f, logger)
			if f.calls != 1 || f.got.Password != secretPw {
				t.Fatalf("bootstrap non chiamato come atteso: %+v", f)
			}
			if strings.Contains(buf.String(), secretPw) {
				t.Fatalf("password nel log:\n%s", buf.String())
			}
			if buf.Len() == 0 {
				t.Fatal("atteso almeno un log")
			}
		})
	}
}

func TestBootstrapAdminSkippedWithoutPassword(t *testing.T) {
	f := &fakeBootstrapper{}
	cfg := bootstrapCfg()
	cfg.AdminPassword = ""
	var buf bytes.Buffer
	if err := bootstrapAdmin(context.Background(), cfg, f, slog.New(slog.NewJSONHandler(&buf, nil))); err != nil || f.calls != 0 {
		t.Fatalf("err=%v calls=%d", err, f.calls)
	}
}
