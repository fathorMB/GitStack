package api

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Test di contratto dei database gestiti (v3-d, GIT-550): regole DB1, DB2,
// DB20, DB22 e l'interruttore d'istanza (DB23). Leggono openapi.yaml riga per
// riga, come mcp_test.go: il modulo api non ha dipendenze, quindi si
// appoggiano alla forma fissa del documento (percorsi a 2 spazi, metodi a 4,
// proprieta' dell'operazione a 6).

type dbOp struct {
	method, path, id, block string
}

// databaseOps restituisce le operazioni dei percorsi /databases/* e
// /admin/databases/*, ciascuna col blocco grezzo (proprieta' e responses).
func databaseOps(t *testing.T) []dbOp {
	t.Helper()
	var ops []*dbOp
	var path string
	var cur *dbOp
	inPaths := false
	methodRe := regexp.MustCompile(`^    (get|put|post|delete|patch):\s*$`)
	pathRe := regexp.MustCompile(`^  (/databases\S*|/admin/databases\S*):\s*$`)
	sc := bufio.NewScanner(bytes.NewReader(Spec))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.HasPrefix(line, "paths:") {
			inPaths = true
			continue
		}
		if inPaths && line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "#") {
			break // fine di paths
		}
		if !inPaths {
			continue
		}
		if m := pathRe.FindStringSubmatch(line); m != nil {
			path = m[1]
			cur = nil
			continue
		}
		if path == "" {
			continue
		}
		if m := methodRe.FindStringSubmatch(line); m != nil {
			ops = append(ops, &dbOp{method: strings.ToUpper(m[1]), path: path})
			cur = ops[len(ops)-1]
			continue
		}
		if cur != nil {
			cur.block += line + "\n"
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	var out []dbOp
	for _, op := range ops {
		m := regexp.MustCompile(`(?m)^      operationId: (\w+)\s*$`).FindStringSubmatch(op.block)
		if m == nil {
			t.Fatalf("%s %s: operationId mancante", op.method, op.path)
		}
		op.id = m[1]
		if !strings.Contains(op.block, "tags: [databases]") {
			t.Errorf("%s %s (%s): tag databases atteso", op.method, op.path, op.id)
		}
		out = append(out, *op)
	}
	return out
}

// Le operazioni previste dall'ADR 0003 (decisioni 3-7): elenco, creazione,
// dettaglio, modifica (dimensione, CPU/RAM), eliminazione, collegamenti alle
// app, credenziali e rotazione, backup (elenco, avvio, ripristino, retention
// nelle impostazioni d'istanza), accesso dalla LAN, log e metriche e le
// impostazioni d'istanza con l'interruttore.
func TestDatabases_OperazioniPrevisteDallADR(t *testing.T) {
	want := []string{
		"listDatabases", "createDatabase", "getDatabase", "updateDatabase", "deleteDatabase",
		"rotateDatabaseCredentials",
		"listDatabaseLinks", "createDatabaseLink", "deleteDatabaseLink", "rotateDatabaseLink",
		"listDatabaseBackups", "createDatabaseBackup", "restoreDatabaseBackup",
		"getDatabaseLan", "setDatabaseLan",
		"getDatabaseLogs", "getDatabaseMetrics",
		"getAdminDatabasesSettings", "updateAdminDatabasesSettings",
	}
	have := map[string]bool{}
	for _, op := range databaseOps(t) {
		have[op.id] = true
	}
	for _, id := range want {
		if !have[id] {
			t.Errorf("operazione %s assente dal contratto", id)
		}
	}
}

// DB2: le operazioni di lettura dichiaranti i permessi — chi legge e chi no.
// Owner (l'utente o gli amministratori della sua organizzazione): 200; un
// membro dell'organizzazione che non e' amministratore e un estraneo: 404
// (come i repo privati); l'amministratore dell'istanza legge tutto. Lo scope
// del token e' read:resource e la lettura non e' distruttiva (M3). Le
// operazioni /admin/databases sono di amministrazione: amministratore
// dell'istanza, 403 agli altri.
func TestDatabases_PermessiSulleOperazioniDiLettura(t *testing.T) {
	reads := 0
	for _, op := range databaseOps(t) {
		if op.method != "GET" {
			continue
		}
		reads++
		where := op.method + " " + op.path + " (" + op.id + ")"
		if !strings.Contains(op.block, "destructive: false") {
			t.Errorf("%s: una lettura non e' distruttiva (M3)", where)
		}
		if strings.HasPrefix(op.path, "/admin/") {
			if !strings.Contains(op.block, `x-required-scopes: ["write:resource"]`) {
				t.Errorf("%s: un'amministrazione dichiara scope write:resource come per le app", where)
			}
			if !strings.Contains(op.block, "403") {
				t.Errorf("%s: la descrizione non dichiara il divieto per i non amministratori", where)
			}
			continue
		}
		if !strings.Contains(op.block, `x-required-scopes: ["read:resource"]`) {
			t.Errorf("%s: una lettura dichiara scope read:resource", where)
		}
		text := strings.Join(strings.Fields(op.block), " ")
		for _, phrase := range []string{"amministratori della sua organizzazione", "membro dell'organizzazione", "estran", "404"} {
			if !strings.Contains(text, phrase) {
				t.Errorf("%s: la descrizione non dichiara i permessi (manca %q)", where, phrase)
			}
		}
	}
	if reads == 0 {
		t.Fatal("nessuna operazione di lettura nel tag databases?")
	}
}

// Interruttore d'istanza (DB23), spento di default: con la funzione spenta
// ogni operazione risponde l'errore stabile 422 `databases_disabled` — mai
// 501 (difetto di GIT-547). Il codice e' dichiarato nella descrizione e la
// risposta 422 e' nel contratto.
func TestDatabases_ErroreStabileConFunzioneSpenta(t *testing.T) {
	for _, op := range databaseOps(t) {
		where := op.method + " " + op.path + " (" + op.id + ")"
		if !strings.Contains(op.block, `"422":`) {
			t.Errorf("%s: manca la risposta 422 dello stato disabilitato", where)
		}
		if !strings.Contains(op.block, "databases_disabled") {
			t.Errorf("%s: la descrizione non dichiara il codice d'errore stabile databases_disabled", where)
		}
	}
}

// DB22: le password si vedono solo alla creazione o alla rotazione e non si
// rileggono. Gli schemi *Credentials compaiono nelle risposte di sole quattro
// operazioni; nessun'altra operazione li restituisce.
func TestDatabases_PasswordSoloACreazioneERotazione(t *testing.T) {
	showsPassword := map[string]bool{
		"createDatabase": true, "rotateDatabaseCredentials": true,
		"createDatabaseLink": true, "rotateDatabaseLink": true,
	}
	for _, op := range databaseOps(t) {
		where := op.method + " " + op.path + " (" + op.id + ")"
		if !showsPassword[op.id] && strings.Contains(op.block, "Credentials") {
			t.Errorf("%s: restituisce credenziali, che si vedono solo a creazione o rotazione (DB22)", where)
		}
	}
}

// DB20: la famiglia di eventi database.* ha nomi e schema fissi in
// docs/events.md, i payload non contengono segreti.
func TestDatabases_EventiDocumentatiSenzaSegreti(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "docs", "events.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	for _, name := range []string{
		"database.created", "database.updated", "database.deleted",
		"database.linked", "database.unlinked",
		"database.lan_enabled", "database.lan_disabled",
		"database.backup_completed", "database.backup_failed", "database.restored",
	} {
		if !strings.Contains(doc, name) {
			t.Errorf("docs/events.md non documenta l'evento %s", name)
		}
	}
	if !strings.Contains(doc, "quota.warning") {
		t.Error("docs/events.md non documenta l'avviso quota.warning")
	}
	for _, phrase := range []string{"segret", "password"} {
		if !strings.Contains(doc, phrase) {
			t.Errorf("docs/events.md non dice che i payload sono senza %s", phrase)
		}
	}
}
