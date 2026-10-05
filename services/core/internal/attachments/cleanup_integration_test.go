//go:build integration

package attachments_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/attachments"
	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type env struct {
	t     *testing.T
	pool  *pgxpool.Pool
	st    *store.Store
	disk  *attachments.Disk
	repo  uuid.UUID
	owner uuid.UUID
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	e := &env{t: t, pool: pool, st: store.New(pool), disk: &attachments.Disk{Dir: t.TempDir()}, repo: uuid.New(), owner: uuid.New()}
	e.newRepo(e.repo, "r")
	return e
}

func (e *env) newRepo(id uuid.UUID, name string) {
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `INSERT INTO core.resources (id, type, name) VALUES ($1, 'repo', $2)`, id, "alice/"+name); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO core.repositories (resource_id, owner_type, owner_id, name) VALUES ($1, 'user', $2, $3)`, id, e.owner, name); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) issue(repo uuid.UUID, number int) uuid.UUID {
	id := uuid.New()
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO core.issues (id, repo_id, number, title, author_id) VALUES ($1, $2, $3, 't', $4)`, id, repo, number, e.owner); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// add salva file e riga, come l'upload, con createdAt dato.
func (e *env) add(repo, uploader uuid.UUID, createdAt time.Time) uuid.UUID {
	e.t.Helper()
	id := uuid.New()
	ct, n, err := e.disk.Save(repo, id, strings.NewReader("log\n"), 100)
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.st.CreateAttachment(context.Background(),
		store.Attachment{ID: id, RepoID: repo, UploaderID: uploader, Filename: "a.log", ContentType: ct, Size: n}, createdAt); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) fileExists(repo, id uuid.UUID) bool {
	f, err := e.disk.Open(repo, id)
	if err == nil {
		_ = f.Close()
		return true
	}
	if !errors.Is(err, os.ErrNotExist) {
		e.t.Fatal(err)
	}
	return false
}

func (e *env) rows() int {
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.issue_attachments`).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// Pulizia con il tempo iniettato: dopo 24 ore sparisce l'orfano (file e
// riga); un allegato collegato resta, qualunque sia la sua età.
func TestCleaner_OrfaniConTempoIniettato(t *testing.T) {
	e := newEnv(t)
	t0 := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	orphan := e.add(e.repo, e.owner, t0)
	young := e.add(e.repo, e.owner, t0.Add(20*time.Hour))
	linked := e.add(e.repo, e.owner, t0)
	issue := e.issue(e.repo, 1)
	tx, err := e.st.BeginTx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.LinkAttachments(context.Background(), tx, e.repo, e.owner, issue, nil, []uuid.UUID{linked}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}

	now := t0.Add(23*time.Hour + 59*time.Minute)
	c := &attachments.Cleaner{Store: e.st, Disk: e.disk, Now: func() time.Time { return now }}
	if n, err := c.RunOnce(context.Background()); err != nil || n != 0 {
		t.Fatalf("prima delle 24 ore: %d %v", n, err)
	}
	now = t0.Add(24*time.Hour + time.Minute)
	if n, err := c.RunOnce(context.Background()); err != nil || n != 1 {
		t.Fatalf("dopo 24 ore: %d %v", n, err)
	}
	if e.fileExists(e.repo, orphan) || !e.fileExists(e.repo, young) || !e.fileExists(e.repo, linked) {
		t.Error("file rimasti/tolti in modo errato")
	}
	if e.rows() != 2 {
		t.Errorf("righe %d, volute 2", e.rows())
	}
	// Il giovane scade più tardi; il collegato mai, nemmeno dopo un anno.
	now = t0.Add(365 * 24 * time.Hour)
	if n, _ := c.RunOnce(context.Background()); n != 1 {
		t.Fatalf("giovane: %d", n)
	}
	if !e.fileExists(e.repo, linked) || e.rows() != 1 {
		t.Error("l'allegato collegato non deve sparire")
	}
	// TTL configurabile.
	short := e.add(e.repo, e.owner, now)
	c.TTL = time.Hour
	now = now.Add(2 * time.Hour)
	if n, _ := c.RunOnce(context.Background()); n != 1 || e.fileExists(e.repo, short) {
		t.Errorf("TTL di un'ora non applicato")
	}
	// Riga con file già assente: la riga si elimina comunque.
	gone := e.add(e.repo, e.owner, now.Add(-3*time.Hour))
	if err := e.disk.Remove(e.repo, gone); err != nil {
		t.Fatal(err)
	}
	if n, err := c.RunOnce(context.Background()); err != nil || n != 1 {
		t.Fatalf("file assente: %d %v", n, err)
	}
}

// Più lotti: oltre i 100 per corsa si continua finché non resta niente.
func TestCleaner_PiuLotti(t *testing.T) {
	e := newEnv(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 130; i++ {
		e.add(e.repo, e.owner, t0)
	}
	c := &attachments.Cleaner{Store: e.st, Disk: e.disk, Now: func() time.Time { return t0.Add(48 * time.Hour) }}
	if n, err := c.RunOnce(context.Background()); err != nil || n != 130 {
		t.Fatalf("%d %v", n, err)
	}
	if e.rows() != 0 {
		t.Error("righe rimaste")
	}
}

func TestLinkAttachments(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	now := time.Now()
	other := uuid.New()
	e.newRepo(other, "altro")

	a, b := e.add(e.repo, e.owner, now), e.add(e.repo, e.owner, now)
	foreign := e.add(e.repo, uuid.New(), now) // di un altro utente
	elsewhere := e.add(other, e.owner, now)   // di un altro repo
	issue := e.issue(e.repo, 1)

	link := func(ids ...uuid.UUID) error {
		tx, err := e.st.BeginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := store.LinkAttachments(ctx, tx, e.repo, e.owner, issue, nil, ids); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	linkedCount := func() int {
		var n int
		_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM core.issue_attachments WHERE issue_id IS NOT NULL`).Scan(&n)
		return n
	}

	// Uno non collegabile fa fallire tutto, senza collegarne nessuno.
	for name, bad := range map[string]uuid.UUID{"altro utente": foreign, "altro repo": elsewhere, "inesistente": uuid.New()} {
		if err := link(a, bad); !errors.Is(err, store.ErrAttachmentNotLinkable) {
			t.Errorf("%s: %v", name, err)
		}
		if linkedCount() != 0 {
			t.Fatalf("%s: collegamento parziale", name)
		}
	}
	// Elenco vuoto: niente da fare. Ripetizioni: contano una volta.
	if err := link(); err != nil {
		t.Fatal(err)
	}
	if err := link(a, a, b); err != nil || linkedCount() != 2 {
		t.Fatalf("collegamento: %v, %d", err, linkedCount())
	}
	// Già collegati: non si collegano due volte.
	if err := link(a); !errors.Is(err, store.ErrAttachmentNotLinkable) {
		t.Errorf("già collegato: %v", err)
	}
	// A un commento: serve la issue.
	comment := uuid.New()
	if _, err := e.pool.Exec(ctx, `INSERT INTO core.issue_comments (id, issue_id, author_id, body) VALUES ($1, $2, $3, 'c')`, comment, issue, e.owner); err != nil {
		t.Fatal(err)
	}
	c := e.add(e.repo, e.owner, now)
	tx, _ := e.st.BeginTx(ctx)
	if err := store.LinkAttachments(ctx, tx, e.repo, e.owner, issue, &comment, []uuid.UUID{c}); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit(ctx)
	var got *uuid.UUID
	if err := e.pool.QueryRow(ctx, `SELECT comment_id FROM core.issue_attachments WHERE id = $1`, c).Scan(&got); err != nil || got == nil || *got != comment {
		t.Errorf("comment_id: %v %v", got, err)
	}
}
