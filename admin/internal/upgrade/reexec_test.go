package upgrade

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

// GIT-490: un upgrade che fallisce, lanciato da un binario vecchio, fa eseguire il rollback al
// binario di destinazione (re-exec). Qui il «binario nuovo» è RunReexec con le stesse
// dipendenze; il punto è che il processo vecchio gli passa lo stato giusto e ne prende l'esito.
func TestRollbackRunsInTargetBinary(t *testing.T) {
	w := newWorld(t)
	w.healthy = func() bool { return !w.helm.upgraded || w.helm.rolled }
	var got []*ReexecRequest
	var stagedBin string
	w.o.Reexec = func(ctx context.Context, staged string, req *ReexecRequest) (*ReexecReply, error) {
		got = append(got, req)
		if b, err := os.ReadFile(staged); err != nil || string(b) != newBin {
			t.Errorf("il binario passato al re-exec non è quello di destinazione: %q %v", b, err)
		}
		stagedBin = staged
		// Il processo vecchio non deve aver toccato il cluster prima del re-exec.
		if w.helm.rolled {
			t.Error("rollback già eseguito dal processo vecchio")
		}
		nuovo := *w.o
		nuovo.Reexec = nil
		return RunReexec(ctx, &nuovo, req)
	}
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != RolledBack {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(got) != 1 || got[0].Mode != ReexecRollback || got[0].PrevRev != 4 || !got[0].HelmRan || got[0].BackupPath == "" || got[0].Before == "" {
		t.Fatalf("richiesta: %+v", got)
	}
	if stagedBin == "" {
		t.Fatal("re-exec non chiamato")
	}
	if !res.RolledBackChart || !w.helm.has("rollback gitstack 4") {
		t.Errorf("rollback non eseguito: chart=%v chiamate=%v", res.RolledBackChart, w.helm.calls)
	}
	if !strings.Contains(strings.Join(res.Summary, "\n"), "dati intatti") {
		t.Errorf("riepilogo: %v", res.Summary)
	}
	// i file locali li ripristina comunque il processo vecchio
	w.assertUntouched()
}

// Il binario di destinazione non supporta il re-exec (destinazione precedente a GIT-490):
// si ripiega sulla logica interna, il rollback riesce lo stesso.
func TestRollbackFallsBackWithoutReexec(t *testing.T) {
	for name, rerr := range map[string]error{"non supportato": ErrNoReexec, "fallito": errors.New("exit 70")} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			w.healthy = func() bool { return !w.helm.upgraded || w.helm.rolled }
			calls := 0
			w.o.Reexec = func(context.Context, string, *ReexecRequest) (*ReexecReply, error) {
				calls++
				return nil, rerr
			}
			res, err := Run(context.Background(), w.o)
			if err != nil || res.Outcome != RolledBack || !res.RolledBackChart {
				t.Fatalf("res=%+v err=%v", res, err)
			}
			if calls != 1 || !w.helm.has("rollback gitstack 4") {
				t.Errorf("re-exec=%d chiamate=%v", calls, w.helm.calls)
			}
			w.assertUntouched()
		})
	}
}

// Il rollback non riuscito nel binario nuovo resta Broken nel processo vecchio.
func TestReexecBrokenRollbackIsReported(t *testing.T) {
	w := newWorld(t)
	w.o.Reexec = func(context.Context, string, *ReexecRequest) (*ReexecReply, error) {
		return &ReexecReply{Outcome: Broken, RollbackErr: "helm rollback: boom", Summary: []string{"rollback NON riuscito"}}, nil
	}
	w.helm.onUp = func() error { return errors.New("exit 1") }
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Broken || res.RollbackErr != "helm rollback: boom" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

// Dopo un upgrade riuscito anche i passi finali (GIT-494 compreso) li esegue il binario nuovo.
func TestPostUpgradeStepsRunInTargetBinary(t *testing.T) {
	w := newWorld(t)
	var got *ReexecRequest
	w.o.Reexec = func(ctx context.Context, _ string, req *ReexecRequest) (*ReexecReply, error) {
		got = req
		return &ReexecReply{Outcome: Upgraded, Summary: []string{"passo finale del binario nuovo"}}, nil
	}
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if got == nil || got.Mode != ReexecPost || got.To != "sha-"+newSHA {
		t.Fatalf("richiesta: %+v", got)
	}
	if !strings.Contains(strings.Join(res.Summary, "\n"), "passo finale del binario nuovo") {
		t.Errorf("riepilogo: %v", res.Summary)
	}
	for _, c := range w.helm.calls {
		if strings.Contains(c, "get namespace") {
			t.Errorf("il processo vecchio ha eseguito i passi finali: %s", c)
		}
	}
}
