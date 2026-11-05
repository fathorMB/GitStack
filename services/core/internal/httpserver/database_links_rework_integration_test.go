//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fathorMB/GitStack/services/core/internal/databases"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Rework 1 di GIT-552: rotazione riprendibile e scollegamento ripetuto non distruttivo.

// flakySealer fa fallire il Seal numero failAt (1 = il primo): simula un errore di scrittura dei segreti.
type flakySealer struct {
	store.AppSealer
	mu     sync.Mutex
	n      int
	failAt int
}

func (f *flakySealer) Seal(id uuid.UUID, v string) ([]byte, []byte, string, error) {
	f.mu.Lock()
	f.n++
	fail := f.n == f.failAt
	f.mu.Unlock()
	if fail {
		return nil, nil, "", fmt.Errorf("seal: simulato")
	}
	return f.AppSealer.Seal(id, v)
}

func (e *linkEnv) dbRow() store.ManagedDB {
	e.t.Helper()
	d, err := e.st.GetManagedDBByID(context.Background(), uuid.UUID(e.db.Id))
	if err != nil {
		e.t.Fatal(err)
	}
	return d
}

func (e *linkEnv) intentExists(linkID uuid.UUID) bool {
	_, err := e.kube.CoreV1().Secrets(databases.Namespace(uuid.UUID(e.db.Id))).Get(context.Background(), databases.SecretLinkRotatePrefix+linkID.String(), metav1.GetOptions{})
	return err == nil
}

// restart simula il riavvio di core: un controller nuovo sullo stesso store e sullo stesso cluster.
func (e *linkEnv) restart() *databases.Controller {
	return databases.New(e.st, e.kube, databases.Config{SQL: e.spy, CADir: e.ca, Images: map[int]databases.Image{}, Secrets: e.keys,
		Unlink: databases.UnlinkOptions{Timeout: 150 * time.Millisecond, Every: 10 * time.Millisecond}, SweepEvery: time.Nanosecond})
}

func (e *linkEnv) rolePassword(role string) string {
	e.spy.mu.Lock()
	defer e.spy.mu.Unlock()
	return e.spy.rolePw[role]
}

// converged verifica ruolo, segreti, versione e intento dopo una rotazione arrivata in fondo.
func (e *linkEnv) converged(appID uuid.UUID, c openapi.DatabaseLinkCreated, versionsBefore int) string {
	e.t.Helper()
	role, got := c.Credentials.Username, e.secrets(appID)
	pw := e.rolePassword(role)
	if pw == "" || pw == c.Credentials.Password {
		e.t.Fatalf("il ruolo deve avere la password nuova: %q (vecchia %q)", pw, c.Credentials.Password)
	}
	if got["PGPASSWORD"] != pw || !strings.Contains(got["DATABASE_URL"], pw) || strings.Contains(got["DATABASE_URL"], c.Credentials.Password) {
		e.t.Fatalf("segreti dell'app non coerenti con il ruolo: %v", got)
	}
	if e.versionCount(appID) != versionsBefore+1 {
		e.t.Fatalf("serve esattamente una versione nuova: %d -> %d", versionsBefore, e.versionCount(appID))
	}
	if e.intentExists(uuid.UUID(c.Link.Id)) {
		e.t.Fatal("l'intento di rotazione doveva sparire")
	}
	return pw
}

// DB12: errore DOPO l'ALTER ROLE (chiusura delle sessioni). Il ruolo ha gia' la password nuova, i segreti la
// vecchia: l'intento persistente permette a un controller NUOVO (core riavviato) di riportare tutto a convergenza.
func TestDatabaseLinks_RotazioneRiprendibileDopoErroreSulleSessioni(t *testing.T) {
	e := newLinkEnv(t)
	appID, _ := e.mkApp("alice", "web")
	c := e.link("alice", "alice", "web", "rw", http.StatusCreated)
	link, _ := e.st.GetManagedDBLink(context.Background(), uuid.UUID(e.db.Id), uuid.UUID(c.Link.Id))
	versions := e.versionCount(appID)

	e.spy.failTerminate = 1
	if _, err := e.ctl.RotateLink(context.Background(), e.keys, e.dbRow(), link, nil); err == nil {
		t.Fatal("la rotazione doveva fallire")
	}
	if e.rolePassword(c.Credentials.Username) == c.Credentials.Password || e.secrets(appID)["PGPASSWORD"] != c.Credentials.Password {
		t.Fatal("precondizione: ALTER ROLE fatto, segreti ancora vecchi")
	}
	if !e.intentExists(link.ID) {
		t.Fatal("l'intento deve essere persistito prima dell'effetto esterno")
	}
	// Core riparte: un giro del nuovo controller converge, senza nessuna richiesta dell'utente.
	e.restart().ResumeRotations(context.Background(), e.dbRow())
	pw := e.converged(appID, c, versions)
	// Il ruolo ha solo la nuova (lo spy tiene una password per ruolo) e un altro giro non cambia niente.
	e.restart().ResumeRotations(context.Background(), e.dbRow())
	if e.versionCount(appID) != versions+1 || e.rolePassword(c.Credentials.Username) != pw {
		t.Fatal("un secondo giro deve essere un no-op")
	}
	if e.getDB().LinkCount != 1 {
		t.Fatal("il collegamento (e i dati) restano")
	}
}

// DB12: errore di scrittura dei segreti (Seal) dopo l'ALTER ROLE. Una nuova richiesta di rotazione riusa la
// password dell'intento e converge; in alternativa converge il controller da solo.
func TestDatabaseLinks_RotazioneRiprendibileDopoErroreSuiSegreti(t *testing.T) {
	for _, viaRetry := range []bool{false, true} {
		t.Run(fmt.Sprintf("viaRichiesta=%v", viaRetry), func(t *testing.T) {
			e := newLinkEnv(t)
			appID, _ := e.mkApp("alice", "web")
			c := e.link("alice", "alice", "web", "rw", http.StatusCreated)
			link, _ := e.st.GetManagedDBLink(context.Background(), uuid.UUID(e.db.Id), uuid.UUID(c.Link.Id))
			versions := e.versionCount(appID)

			// Seal 1 = intento, Seal 2 = prima variabile dei segreti dell'app: la transazione annulla tutto.
			bad := &flakySealer{AppSealer: e.keys, failAt: 2}
			if _, err := e.ctl.RotateLink(context.Background(), bad, e.dbRow(), link, nil); err == nil {
				t.Fatal("la rotazione doveva fallire")
			}
			if e.versionCount(appID) != versions || e.secrets(appID)["PGPASSWORD"] != c.Credentials.Password || !e.intentExists(link.ID) {
				t.Fatal("dopo l'errore: nessuna versione nuova, segreti vecchi, intento presente")
			}
			intentPw := e.rolePassword(c.Credentials.Username)
			if viaRetry {
				creds, err := e.restart().RotateLink(context.Background(), e.keys, e.dbRow(), link, nil)
				if err != nil || creds.Password != intentPw {
					t.Fatalf("la nuova richiesta riusa la password dell'intento: %v %q vs %q", err, creds.Password, intentPw)
				}
			} else {
				e.restart().ResumeRotations(context.Background(), e.dbRow())
			}
			if pw := e.converged(appID, c, versions); pw != intentPw {
				t.Fatalf("password finale %q diversa dall'intento %q", pw, intentPw)
			}
		})
	}
}

// Caso nominale: una rotazione senza errori lascia un solo effetto e nessun intento; una seconda rotazione ne fa un'altra.
func TestDatabaseLinks_RotazioneNominaleSenzaIntentoResiduo(t *testing.T) {
	e := newLinkEnv(t)
	appID, _ := e.mkApp("alice", "web")
	c := e.link("alice", "alice", "web", "rw", http.StatusCreated)
	versions := e.versionCount(appID)
	path := "/databases/alice/shop/links/" + uuid.UUID(c.Link.Id).String() + "/rotate"
	e.want(e.do(http.MethodPost, path, "alice", ""), http.StatusOK, "")
	first := e.converged(appID, c, versions)
	e.want(e.do(http.MethodPost, path, "alice", ""), http.StatusOK, "")
	if second := e.rolePassword(c.Credentials.Username); second == first || e.secrets(appID)["PGPASSWORD"] != second || e.versionCount(appID) != versions+2 {
		t.Fatal("la seconda rotazione deve produrre una password e una versione nuove")
	}
}

// Scollegamento ripetuto: uno snapshot obsoleto del collegamento A non deve toccare le variabili di un
// collegamento B successivo, ne' segreti manuali con gli stessi nomi.
func TestDatabaseLinks_ScollegamentoObsoletoNonToccaIlResto(t *testing.T) {
	e := newLinkEnv(t)
	appID, _ := e.mkApp("alice", "web")
	ctx := context.Background()
	ref := store.ManagedDBAppRef{Owner: "alice", Repo: "web", Name: "web"}
	a := e.link("alice", "alice", "web", "rw", http.StatusCreated)
	staleA, err := e.st.GetManagedDBLink(ctx, uuid.UUID(e.db.Id), uuid.UUID(a.Link.Id))
	if err != nil {
		t.Fatal(err)
	}
	dbA := e.dbRow()
	e.want(e.do(http.MethodDelete, "/databases/alice/shop/links/"+uuid.UUID(a.Link.Id).String(), "alice", ""), http.StatusAccepted, "")
	if len(e.secrets(appID)) != 0 || e.getDB().LinkCount != 0 {
		t.Fatal("il primo scollegamento toglie variabili e collegamento")
	}

	// Segreto manuale con uno di quei nomi (consentito: nessun collegamento) + snapshot obsoleto.
	base := "/repos/alice/web/apps/web"
	e.want(e.do(http.MethodPut, base+"/secrets/PGUSER", "alice", `{"value":"manuale"}`), http.StatusOK, "")
	versions, deploys := e.versionCount(appID), e.deployCount(appID)
	if _, made, err := e.st.CompleteDBUnlink(ctx, dbA, staleA, ref, "unlink", nil, e.clock.Now()); err != nil || made {
		t.Fatalf("snapshot obsoleto: made=%v err=%v", made, err)
	}
	if e.secrets(appID)["PGUSER"] != "manuale" || e.versionCount(appID) != versions || e.deployCount(appID) != deploys {
		t.Fatal("lo scollegamento obsoleto ha toccato i segreti manuali, la versione o il deploy")
	}
	e.want(e.do(http.MethodDelete, base+"/secrets/PGUSER", "alice", ""), http.StatusNoContent, "")

	// L'app viene collegata a un secondo database B; poi arriva di nuovo lo scollegamento di A (store e controller).
	b := (&dbEnv{appsEnv: e.appsEnv}).create("alice", "alice", `{"name":"bdue","pgMajor":17}`, http.StatusAccepted).Database
	if err := e.st.SetManagedDBStatus(ctx, uuid.UUID(b.Id), store.DBRunning, nil, e.clock.Now()); err != nil {
		t.Fatal(err)
	}
	rec := e.do(http.MethodPost, "/databases/alice/bdue/links", "alice", `{"app":{"owner":"alice","repo":"web","name":"web"},"mode":"rw"}`)
	e.want(rec, http.StatusCreated, "")
	var lb openapi.DatabaseLinkCreated
	_ = json.Unmarshal(rec.Body.Bytes(), &lb)
	before := e.secrets(appID)
	versions, deploys = e.versionCount(appID), e.deployCount(appID)

	if _, made, err := e.st.CompleteDBUnlink(ctx, dbA, staleA, ref, "unlink", nil, e.clock.Now()); err != nil || made {
		t.Fatalf("A obsoleto sul store: made=%v err=%v", made, err)
	}
	if done, err := e.ctl.Unlink(ctx, dbA, staleA, nil); err != nil || !done {
		t.Fatalf("A obsoleto dal controller: done=%v err=%v", done, err)
	}
	after := e.secrets(appID)
	if len(after) != len(store.ManagedDBEnvNames) || after["PGPASSWORD"] != before["PGPASSWORD"] || after["PGPASSWORD"] != lb.Credentials.Password {
		t.Fatalf("le variabili di B sono cambiate: %v", after)
	}
	if e.versionCount(appID) != versions || e.deployCount(appID) != deploys {
		t.Fatal("nessuna versione o deploy nuovo per uno scollegamento obsoleto")
	}
	links, err := e.st.ListAppDBLinks(ctx, appID)
	if err != nil || len(links) != 1 || links[0].ID != uuid.UUID(lb.Link.Id) || links[0].State != "active" {
		t.Fatalf("il collegamento B deve restare attivo: %+v %v", links, err)
	}
}
