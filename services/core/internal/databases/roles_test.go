package databases

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// recorder e' un SQLRunner finto: registra gli statement nell'ordine e risponde alle query sulle sessioni.
type recorder struct {
	mu       sync.Mutex
	stmts    []string
	sessions []int // risposte successive di count(*) su pg_stat_activity (l'ultima si ripete)
	size     int64
	existing bool // il database esiste gia'
}

func (r *recorder) Exec(_ context.Context, _ store.ManagedDB, database, sql string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stmts = append(r.stmts, database+"| "+strings.Join(strings.Fields(sql), " "))
	return nil
}

func (r *recorder) Query(_ context.Context, _ store.ManagedDB, database, sql string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	one := strings.Join(strings.Fields(sql), " ")
	r.stmts = append(r.stmts, database+"| "+one)
	switch {
	case strings.Contains(one, "count(*) FROM pg_stat_activity"):
		n := 0
		if len(r.sessions) > 0 {
			n = r.sessions[0]
			if len(r.sessions) > 1 {
				r.sessions = r.sessions[1:]
			}
		}
		return []string{itoa(n)}, nil
	case strings.Contains(one, "pg_database_size"):
		return []string{itoa64(r.size)}, nil
	case strings.Contains(one, "FROM pg_database"):
		if r.existing {
			return []string{"1"}, nil
		}
	}
	return nil, nil
}

func itoa(n int) string     { return fmtInt(int64(n)) }
func itoa64(n int64) string { return fmtInt(n) }
func fmtInt(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func (r *recorder) index(substr string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, s := range r.stmts {
		if strings.Contains(s, substr) {
			return i
		}
	}
	return -1
}

// DB12: l'ordine dello scollegamento e' obbligatorio: NOLOGIN, poi pg_terminate_backend fino a zero sessioni,
// poi DROP OWNED e DROP ROLE.
func TestDatabaseLinks_OrdineDiScollegamento(t *testing.T) {
	r := &recorder{sessions: []int{2, 1, 0}}
	d := testDB()
	link := uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	if err := DropLinkRole(context.Background(), r, d, link, UnlinkOptions{Every: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	role := `"l_aaaaaaaabbbbccccddddeeeeeeeeeeee"`
	nologin := r.index("ALTER ROLE " + role + " NOLOGIN")
	terminate := r.index("pg_terminate_backend")
	dropOwned := r.index("DROP OWNED BY " + role)
	dropRole := r.index("DROP ROLE " + role)
	if nologin < 0 || terminate < 0 || dropOwned < 0 || dropRole < 0 {
		t.Fatalf("mancano passi: %d %d %d %d\n%s", nologin, terminate, dropOwned, dropRole, strings.Join(r.stmts, "\n"))
	}
	if !(nologin < terminate && terminate < dropOwned && dropOwned < dropRole) {
		t.Fatalf("ordine sbagliato: NOLOGIN=%d terminate=%d DROP OWNED=%d DROP ROLE=%d", nologin, terminate, dropOwned, dropRole)
	}
	// Ripetuto finche' pg_stat_activity non mostra piu' sessioni: 3 giri (2, 1, 0 sessioni).
	n := 0
	for _, s := range r.stmts {
		if strings.Contains(s, "pg_terminate_backend") {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("pg_terminate_backend ripetuto %d volte, volute 3", n)
	}
	// DROP OWNED gira nel database dell'owner, DROP ROLE e NOLOGIN nel database di servizio.
	if !strings.HasPrefix(r.stmts[dropOwned], "shop| ") {
		t.Fatalf("DROP OWNED nel database sbagliato: %s", r.stmts[dropOwned])
	}
}

// Con sessioni che non si chiudono lo scollegamento non si dichiara completo: il ruolo resta (`unlinking`).
func TestDatabaseLinks_SessioniResidueNonCompletano(t *testing.T) {
	r := &recorder{sessions: []int{3}}
	err := DropLinkRole(context.Background(), r, testDB(), uuid.New(), UnlinkOptions{Timeout: 30 * time.Millisecond, Every: 5 * time.Millisecond})
	if !errors.Is(err, ErrSessionsRemain) {
		t.Fatalf("errore: %v", err)
	}
	if r.index("DROP ROLE") >= 0 || r.index("DROP OWNED") >= 0 {
		t.Fatal("il ruolo non si elimina finche' ci sono sessioni")
	}
}

// DB9: il ruolo di un collegamento rw e' membro dell'owner con SET role; ro del gruppo di sola lettura; mai
// CREATEROLE/CREATEDB/superuser.
func TestDatabaseLinks_RuoloPerCollegamento(t *testing.T) {
	d := testDB()
	for _, tc := range []struct {
		mode, wantGrant string
		wantSet         bool
	}{
		{ModeRW, `GRANT "shop_owner" TO`, true}, {ModeRO, `GRANT "shop_ro" TO`, false},
	} {
		r := &recorder{}
		id := uuid.New()
		if err := CreateLinkRole(context.Background(), r, d, id, tc.mode, "pw"); err != nil {
			t.Fatal(err)
		}
		all := strings.Join(r.stmts, "\n")
		for _, must := range []string{"NOSUPERUSER", "NOCREATEDB", "NOCREATEROLE", tc.wantGrant} {
			if !strings.Contains(all, must) {
				t.Errorf("%s: manca %q in %s", tc.mode, must, all)
			}
		}
		if strings.Contains(all, "SET role") != tc.wantSet {
			t.Errorf("%s: SET role atteso=%v: %s", tc.mode, tc.wantSet, all)
		}
	}
	if err := CreateLinkRole(context.Background(), &recorder{}, d, uuid.New(), "admin", "pw"); err == nil {
		t.Fatal("una modalita' sconosciuta va rifiutata")
	}
}

func TestQuoting(t *testing.T) {
	if QuoteLiteral("a'b") != `'a''b'` || QuoteIdent(`a"b`) != `"a""b"` {
		t.Fatal("citazione errata")
	}
}
