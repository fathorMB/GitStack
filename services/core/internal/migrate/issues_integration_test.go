//go:build integration

package migrate_test

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// newIssueRepo crea un repo con la riga del contatore, come fa la creazione.
func newIssueRepo(t *testing.T, pool *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	res := newResource(t, pool, "repo", "alice/"+name)
	if err := insertRepo(pool, res, uuid.New(), name); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO core.repo_counters (repo_id) VALUES ($1)`, res); err != nil {
		t.Fatal(err)
	}
	return res
}

// takeNumber è il modo prescritto (I1) di prendere #n: UPDATE ... RETURNING
// nella stessa transazione dell'INSERT.
const takeNumber = `UPDATE core.repo_counters SET next_number = next_number + 1
	WHERE repo_id = $1 RETURNING next_number - 1`

func insertIssue(ctx context.Context, tx pgx.Tx, repo uuid.UUID, title string) (int64, error) {
	var n int64
	if err := tx.QueryRow(ctx, takeNumber, repo).Scan(&n); err != nil {
		return 0, err
	}
	_, err := tx.Exec(ctx, `INSERT INTO core.issues (id, repo_id, number, title, author_id)
		VALUES ($1, $2, $3, $4, $5)`, uuid.New(), repo, n, title, uuid.New())
	return n, err
}

func insertPR(ctx context.Context, tx pgx.Tx, repo uuid.UUID) (int64, error) {
	var n int64
	if err := tx.QueryRow(ctx, takeNumber, repo).Scan(&n); err != nil {
		return 0, err
	}
	_, err := tx.Exec(ctx, `INSERT INTO core.pull_requests
		(id, repo_id, number, source_branch, target_branch, title, author_id)
		VALUES ($1, $2, $3, 'feat', 'main', 't', $4)`, uuid.New(), repo, n, uuid.New())
	return n, err
}

func tableExists(t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var ok bool
	if err := pool.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM information_schema.tables
		WHERE table_schema='core' AND table_name=$1)`, name).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	return ok
}

var issueTables = []string{"issues", "issue_comments", "issue_text_versions", "issue_events",
	"issue_labels", "issue_assignees", "issue_attachments", "labels", "milestones"}

func TestMigration0004_SaleEScende(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()

	for _, tbl := range issueTables {
		if !tableExists(t, pool, tbl) {
			t.Fatalf("%s dovrebbe esistere dopo le migrazioni", tbl)
		}
	}
	// Un repo creato prima della migrazione, senza riga del contatore.
	repo := newResource(t, pool, "repo", "alice/old")
	if err := insertRepo(pool, repo, uuid.New(), "old"); err != nil {
		t.Fatal(err)
	}

	if err := migrate.Down(ctx, pool, dsn, 6); err != nil {
		t.Fatalf("down 0004: %v", err)
	}
	for _, tbl := range issueTables {
		if tableExists(t, pool, tbl) {
			t.Fatalf("%s non dovrebbe esistere dopo il down", tbl)
		}
	}
	var hasMilestoneCol bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_schema='core' AND table_name='repo_counters' AND column_name='next_milestone_number')`).Scan(&hasMilestoneCol); err != nil || hasMilestoneCol {
		t.Fatalf("next_milestone_number dopo il down: %v err=%v", hasMilestoneCol, err)
	}
	// Le PR tornano senza il trigger: il down non lascia funzioni né trigger.
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_proc p JOIN pg_namespace s ON s.oid = p.pronamespace
		WHERE s.nspname = 'core' AND p.proname IN ('check_number_not_taken', 'check_assignee_limit', 'seed_default_labels')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("funzioni rimaste dopo il down: %d err=%v", n, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_trigger WHERE tgname = 'pull_requests_number_not_taken'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("trigger rimasto dopo il down: %d err=%v", n, err)
	}

	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up 0004 dopo down: %v", err)
	}
	for _, tbl := range issueTables {
		if !tableExists(t, pool, tbl) {
			t.Fatalf("%s dovrebbe esistere dopo il nuovo up", tbl)
		}
	}
	// Il repo preesistente ha ora la riga del contatore (backfill).
	var next, nextMs int64
	if err := pool.QueryRow(ctx, `SELECT next_number, next_milestone_number FROM core.repo_counters WHERE repo_id = $1`, repo).
		Scan(&next, &nextMs); err != nil {
		t.Fatalf("contatore del repo preesistente: %v", err)
	}
	if next != 1 || nextMs != 1 {
		t.Fatalf("contatori attesi 1/1, ottenuti %d/%d", next, nextMs)
	}
}

// Issue e PR prendono i numeri dallo stesso contatore in parallelo: nessun
// duplicato, nessun buco (I1).
func TestIssuesEPR_NumeriInParalleloDalloStessoContatore(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	ctx := context.Background()
	repo := newIssueRepo(t, pool, "app")

	const workers = 60
	var wg sync.WaitGroup
	got := make(chan int64, workers)
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tx, err := pool.Begin(ctx)
			if err != nil {
				errs <- err
				return
			}
			defer func() { _ = tx.Rollback(ctx) }()
			var n int64
			if i%2 == 0 {
				n, err = insertIssue(ctx, tx, repo, fmt.Sprintf("issue %d", i))
			} else {
				n, err = insertPR(ctx, tx, repo)
			}
			if err != nil {
				errs <- err
				return
			}
			if err := tx.Commit(ctx); err != nil {
				errs <- err
				return
			}
			got <- n
		}(i)
	}
	wg.Wait()
	close(got)
	close(errs)
	for err := range errs {
		t.Fatalf("transazione fallita: %v", err)
	}
	var nums []int64
	for n := range got {
		nums = append(nums, n)
	}
	sort.Slice(nums, func(i, j int) bool { return nums[i] < nums[j] })
	if len(nums) != workers {
		t.Fatalf("attesi %d numeri, ottenuti %d", workers, len(nums))
	}
	for i, n := range nums {
		if n != int64(i+1) {
			t.Fatalf("numeri non consecutivi o duplicati: %v", nums)
		}
	}
	// Nelle tabelle: nessun numero in comune e la somma torna.
	var issues, prs, shared int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM core.issues WHERE repo_id = $1),
		(SELECT count(*) FROM core.pull_requests WHERE repo_id = $1),
		(SELECT count(*) FROM core.issues i JOIN core.pull_requests p USING (repo_id, number) WHERE i.repo_id = $1)`, repo).
		Scan(&issues, &prs, &shared); err != nil {
		t.Fatal(err)
	}
	if issues != workers/2 || prs != workers/2 || shared != 0 {
		t.Fatalf("issues=%d prs=%d condivisi=%d", issues, prs, shared)
	}
	var next int64
	if err := pool.QueryRow(ctx, `SELECT next_number FROM core.repo_counters WHERE repo_id = $1`, repo).Scan(&next); err != nil || next != workers+1 {
		t.Fatalf("contatore finale %d (atteso %d) err=%v", next, workers+1, err)
	}
}

// Un numero non passato dal contatore che collide con l'altra tabella viene
// rifiutato dal trigger (23505); una transazione annullata non lascia buchi
// riusabili (il numero non si riusa mai).
func TestIssuesEPR_CollisioneEMaiRiuso(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	ctx := context.Background()
	repo := newIssueRepo(t, pool, "app")

	tx, _ := pool.Begin(ctx)
	n, err := insertPR(ctx, tx, repo)
	if err != nil || n != 1 {
		t.Fatalf("PR #1: %d %v", n, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	// Issue con lo stesso numero della PR, senza il contatore.
	_, err = pool.Exec(ctx, `INSERT INTO core.issues (id, repo_id, number, title, author_id) VALUES ($1, $2, 1, 'x', $3)`,
		uuid.New(), repo, uuid.New())
	wantCode(t, err, "23505")
	// E viceversa: PR #2 diretta dopo una issue #2.
	tx, _ = pool.Begin(ctx)
	if n, err = insertIssue(ctx, tx, repo, "due"); err != nil || n != 2 {
		t.Fatalf("issue #2: %d %v", n, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO core.pull_requests (id, repo_id, number, source_branch, target_branch, title, author_id)
		VALUES ($1, $2, 2, 'a', 'b', 't', $3)`, uuid.New(), repo, uuid.New())
	wantCode(t, err, "23505")

	// Rollback: il contatore torna indietro con la transazione, quindi
	// nessun numero e' stato consumato in modo visibile.
	tx, _ = pool.Begin(ctx)
	if n, err = insertIssue(ctx, tx, repo, "annullata"); err != nil || n != 3 {
		t.Fatalf("issue #3: %d %v", n, err)
	}
	_ = tx.Rollback(ctx)
	tx, _ = pool.Begin(ctx)
	if n, err = insertIssue(ctx, tx, repo, "tre"); err != nil || n != 3 {
		t.Fatalf("issue #3 dopo il rollback: %d %v", n, err)
	}
	_ = tx.Commit(ctx)
}

func TestIssues_Vincoli(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	ctx := context.Background()
	repo := newIssueRepo(t, pool, "app")
	other := newIssueRepo(t, pool, "other")
	for i := 0; i < 2; i++ {
		tx, _ := pool.Begin(ctx)
		if _, err := insertIssue(ctx, tx, repo, "t"); err != nil {
			t.Fatal(err)
		}
		_ = tx.Commit(ctx)
	}
	exec := func(q string, args ...any) error {
		_, err := pool.Exec(ctx, q, args...)
		return err
	}
	// UNIQUE(repo_id, number).
	wantCode(t, exec(`INSERT INTO core.issues (id, repo_id, number, title, author_id) VALUES ($1, $2, 1, 'x', $3)`,
		uuid.New(), repo, uuid.New()), "23505")
	// Lo stesso numero in un altro repo e' ammesso.
	if err := exec(`INSERT INTO core.issues (id, repo_id, number, title, author_id) VALUES ($1, $2, 1, 'x', $3)`,
		uuid.New(), other, uuid.New()); err != nil {
		t.Fatalf("stesso numero in un altro repo: %v", err)
	}
	// Titolo vuoto, stato non valido.
	wantCode(t, exec(`INSERT INTO core.issues (id, repo_id, number, title, author_id) VALUES ($1, $2, 9, '', $3)`,
		uuid.New(), repo, uuid.New()), "23514")
	wantCode(t, exec(`INSERT INTO core.issues (id, repo_id, number, title, state, author_id) VALUES ($1, $2, 9, 't', 'merged', $3)`,
		uuid.New(), repo, uuid.New()), "23514")
	// I2: chiusa senza motivo; aperta con motivo.
	wantCode(t, exec(`UPDATE core.issues SET state = 'closed', closed_at = now() WHERE repo_id = $1 AND number = 1`, repo), "23514")
	wantCode(t, exec(`UPDATE core.issues SET close_reason = 'completed' WHERE repo_id = $1 AND number = 1`, repo), "23514")
	// duplicate: serve il numero, che deve esistere nello stesso repo e non
	// essere se stessa.
	closeDup := func(of int) error {
		return exec(`UPDATE core.issues SET state = 'closed', closed_at = now(), close_reason = 'duplicate', duplicate_of = $2
			WHERE repo_id = $1 AND number = 1`, repo, of)
	}
	wantCode(t, closeDup(1), "23514")
	wantCode(t, closeDup(77), "23503")
	if err := closeDup(2); err != nil {
		t.Fatalf("chiusura come duplicato di #2: %v", err)
	}
	wantCode(t, exec(`UPDATE core.issues SET close_reason = 'completed' WHERE repo_id = $1 AND number = 1`, repo), "23514")
	// Riaprire azzera tutto.
	if err := exec(`UPDATE core.issues SET state = 'open', closed_at = NULL, close_reason = NULL, duplicate_of = NULL
		WHERE repo_id = $1 AND number = 1`, repo); err != nil {
		t.Fatalf("riapertura: %v", err)
	}
	if err := exec(`UPDATE core.issues SET state = 'closed', closed_at = now(), close_reason = 'not_planned'
		WHERE repo_id = $1 AND number = 1`, repo); err != nil {
		t.Fatalf("chiusura non pianificata: %v", err)
	}
}

func TestIssues_AssegnatariEtichetteMilestoneRicerca(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	ctx := context.Background()
	repo := newIssueRepo(t, pool, "app")
	tx, _ := pool.Begin(ctx)
	if _, err := insertIssue(ctx, tx, repo, "Crash all'avvio del servizio"); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit(ctx)
	var issue uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM core.issues WHERE repo_id = $1`, repo).Scan(&issue); err != nil {
		t.Fatal(err)
	}

	// I6: dieci assegnatari sì, l'undicesimo no (anche in parallelo).
	for i := 0; i < 10; i++ {
		if _, err := pool.Exec(ctx, `INSERT INTO core.issue_assignees (issue_id, user_id) VALUES ($1, $2)`, issue, uuid.New()); err != nil {
			t.Fatalf("assegnatario %d: %v", i, err)
		}
	}
	_, err := pool.Exec(ctx, `INSERT INTO core.issue_assignees (issue_id, user_id) VALUES ($1, $2)`, issue, uuid.New())
	wantCode(t, err, "23514")

	// I5: etichette predefinite, idempotenti; nome unico senza maiuscole.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, `SELECT core.seed_default_labels($1)`, repo); err != nil {
			t.Fatal(err)
		}
	}
	var labels int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.labels WHERE repo_id = $1`, repo).Scan(&labels); err != nil || labels != 8 {
		t.Fatalf("etichette predefinite: %d err=%v", labels, err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO core.labels (id, repo_id, name, color) VALUES ($1, $2, 'BUG', 'ffffff')`, uuid.New(), repo)
	wantCode(t, err, "23505")
	_, err = pool.Exec(ctx, `INSERT INTO core.labels (id, repo_id, name, color) VALUES ($1, $2, 'a/b', 'ffffff')`, uuid.New(), repo)
	wantCode(t, err, "23514")
	_, err = pool.Exec(ctx, `INSERT INTO core.labels (id, repo_id, name, color) VALUES ($1, $2, 'x', 'zzzzzz')`, uuid.New(), repo)
	wantCode(t, err, "23514")
	if _, err := pool.Exec(ctx, `INSERT INTO core.issue_labels (issue_id, label_id)
		SELECT $1, id FROM core.labels WHERE repo_id = $2 AND name = 'bug'`, issue, repo); err != nil {
		t.Fatal(err)
	}

	// I7: milestone con numero per repo e titolo unico senza maiuscole.
	ms := func(num int, title string) error {
		_, err := pool.Exec(ctx, `INSERT INTO core.milestones (id, repo_id, number, title) VALUES ($1, $2, $3, $4)`,
			uuid.New(), repo, num, title)
		return err
	}
	if err := ms(1, "v1.0"); err != nil {
		t.Fatal(err)
	}
	wantCode(t, ms(2, "V1.0"), "23505")
	wantCode(t, ms(1, "v2.0"), "23505")

	// I10: ricerca testuale PostgreSQL con l'indice GIN.
	var hits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.issues
		WHERE repo_id = $1 AND search @@ plainto_tsquery('simple', 'crash servizio')`, repo).Scan(&hits); err != nil || hits != 1 {
		t.Fatalf("ricerca: %d err=%v", hits, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.issues
		WHERE repo_id = $1 AND search @@ plainto_tsquery('simple', 'inesistente')`, repo).Scan(&hits); err != nil || hits != 0 {
		t.Fatalf("ricerca senza risultati: %d err=%v", hits, err)
	}

	// Allegati: un commento richiede la issue; cascata dalla issue.
	_, err = pool.Exec(ctx, `INSERT INTO core.issue_attachments (id, repo_id, comment_id, uploader_id, filename, content_type, size_bytes)
		VALUES ($1, $2, $3, $4, 'a.png', 'image/png', 10)`, uuid.New(), repo, uuid.New(), uuid.New())
	wantCode(t, err, "23514")
	if _, err := pool.Exec(ctx, `INSERT INTO core.issue_attachments (id, repo_id, uploader_id, filename, content_type, size_bytes)
		VALUES ($1, $2, $3, 'a.png', 'image/png', 10)`, uuid.New(), repo, uuid.New()); err != nil {
		t.Fatalf("allegato non collegato: %v", err)
	}

	// Cascata dal repo fino a tutto.
	if _, err := pool.Exec(ctx, `DELETE FROM core.resources WHERE id = $1`, repo); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range issueTables {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.`+tbl).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s dopo l'eliminazione del repo: %d err=%v", tbl, n, err)
		}
	}
}
