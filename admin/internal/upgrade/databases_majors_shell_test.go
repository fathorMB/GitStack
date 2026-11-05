package upgrade

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/admin/internal/backup"
)

// DB6 (GIT-557): lo script REALE che legge le major in uso (majorsInUseQuery) con un psql finto in una
// cartella temporanea. Un errore di psql alla prima o alla seconda query deve dare exit diverso da zero (mai
// «nessuna major»); tabella assente = successo senza major; major presenti = lettura corretta.

const fakePsql = `#!/bin/sh
sql=""
while [ $# -gt 0 ]; do
  case "$1" in -c) sql="$2" ;; esac
  shift
done
case "$sql" in
  *to_regclass*)
    case "$FAKE_MODE" in
      err1) echo "psql: connessione rifiutata" >&2; exit 2 ;;
      absent) echo f ;;
      weird) echo boh ;;
      *) echo t ;;
    esac ;;
  *pg_major*)
    case "$FAKE_MODE" in
      err2) echo "ERROR: permesso negato" >&2; exit 3 ;;
      *) printf '16\n17\n' ;;
    esac ;;
esac
`

// shellCluster esegue davvero la query delle major con psql finto; il resto è del cluster di prova.
type shellCluster struct {
	*fakeCluster
	dir, mode string
}

func (c *shellCluster) PGExec(ctx context.Context, stdin io.Reader, stdout io.Writer, args ...string) error {
	if !strings.Contains(strings.Join(args, " "), "SELECT DISTINCT pg_major") {
		return c.fakeCluster.PGExec(ctx, stdin, stdout, args...)
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Env = append(os.Environ(), "PATH="+c.dir+string(os.PathListSeparator)+os.Getenv("PATH"), "FAKE_MODE="+c.mode, "POSTGRES_USER=u", "POSTGRES_DB=d")
	var errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = stdout, &errb
	if err := cmd.Run(); err != nil {
		return &execFailure{err: err, stderr: errb.String()}
	}
	return nil
}

type execFailure struct {
	err    error
	stderr string
}

func (e *execFailure) Error() string { return e.err.Error() + ": " + strings.TrimSpace(e.stderr) }

var _ backup.Cluster = (*shellCluster)(nil)

func shellWorld(t *testing.T, mode string) *world {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh non disponibile")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "psql"), []byte(fakePsql), 0o755); err != nil {
		t.Fatal(err)
	}
	w := dbWorld(t)
	signImages(t, w, validIdentity, pgRef16, pgRef17)
	w.o.Cluster = &shellCluster{fakeCluster: w.cl, dir: dir, mode: mode}
	return w
}

func TestMajorsScript_ErroreAllaPrimaQueryFerma(t *testing.T) {
	w := shellWorld(t, "err1")
	if _, err := w.o.databaseMajorsInUse(context.Background()); err == nil {
		t.Fatal("errore di psql alla prima query: atteso un errore, non «nessuna major»")
	}
	refusedNoUpgrade(t, w, "non riesco a leggere le major", "connessione rifiutata")
}

func TestMajorsScript_ErroreAllaSecondaQueryFerma(t *testing.T) {
	w := shellWorld(t, "err2")
	if _, err := w.o.databaseMajorsInUse(context.Background()); err == nil {
		t.Fatal("errore di psql alla seconda query: atteso un errore")
	}
	refusedNoUpgrade(t, w, "non riesco a leggere le major", "permesso negato")
}

func TestMajorsScript_RispostaInattesaFerma(t *testing.T) {
	w := shellWorld(t, "weird")
	if _, err := w.o.databaseMajorsInUse(context.Background()); err == nil {
		t.Fatal("risposta inattesa: atteso un errore")
	}
}

func TestMajorsScript_TabellaAssenteESuccessoSenzaMajor(t *testing.T) {
	w := shellWorld(t, "absent")
	got, err := w.o.databaseMajorsInUse(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatalf("tabella assente: %v %v", got, err)
	}
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestMajorsScript_MajorPresentiLetteCorrettamente(t *testing.T) {
	w := shellWorld(t, "ok")
	got, err := w.o.databaseMajorsInUse(context.Background())
	if err != nil || len(got) != 2 || got[0] != 16 || got[1] != 17 {
		t.Fatalf("major: %v %v", got, err)
	}
}
