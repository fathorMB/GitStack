//go:build integration

package httpserver_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/mailer"
	"github.com/fathorMB/GitStack/services/core/internal/mailer/mailertest"
	"github.com/fathorMB/GitStack/services/core/internal/notify"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// M-06/F (GIT-134): email facoltative via SMTP (C5). Il server SMTP è finto e
// in-process (mailertest). Il tempo si simula spostando email_due_at, non
// aspettando la finestra.

type emailEnv struct {
	*notifyEnv
	smtp *mailertest.Server
	disp *notify.EmailDispatcher
}

func newEmailEnv(t *testing.T) *emailEnv {
	t.Helper()
	e := newNotifyEnvEmail(t, true)
	e.eng.EmailWindow = time.Hour // il raggruppamento si prova con due eventi dentro la finestra
	srv := mailertest.Start(t, &mailertest.Server{})
	d := &notify.EmailDispatcher{
		Pool: e.pool, Users: e.nid, PublicURL: "https://git.example.com",
		Mail:    &mailer.Mailer{Cfg: mailer.Config{Host: srv.Host, Port: srv.Port, Security: mailer.SecurityNone, From: "GitStack <noreply@example.com>"}},
		Backoff: time.Millisecond, MaxAttempts: 3,
	}
	return &emailEnv{notifyEnv: e, smtp: srv, disp: d}
}

// due fa scadere la finestra di tutte le email in attesa.
func (e *emailEnv) due() {
	e.t.Helper()
	e.process()
	e.sql(`UPDATE core.notifications SET email_due_at = clock_timestamp() - interval '1 second' WHERE email_due_at IS NOT NULL AND email_sent_at IS NULL`)
}

// dispatch spedisce le email dovute e ritorna quante ne sono partite.
func (e *emailEnv) dispatch() int {
	e.t.Helper()
	n, err := e.disp.Process(context.Background())
	if err != nil {
		e.t.Fatalf("dispatcher: %v", err)
	}
	return n
}

func (e *emailEnv) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *emailEnv) prefs(user string) openapi.NotificationPreferences {
	e.t.Helper()
	rec := e.do(http.MethodGet, "/user/notification-preferences", user, "")
	e.want(rec, http.StatusOK, "")
	return decodeInto[openapi.NotificationPreferences](e.t, rec)
}

func TestEmail_PerTipoRaggruppateSoloAlleLettereC5(t *testing.T) {
	e := newEmailEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	e.id.grantWrite(repoID, "dave")
	n := e.openWith("app", "bob", "Crash all'avvio", "testo")
	e.clear()

	// Default: email per le menzioni, non per i commenti di chi partecipa.
	// Due menzioni e un commento dentro la finestra: una sola email con le
	// due menzioni.
	e.say("app", n, "carol", "ehi @bob, guarda")
	e.say("app", n, "dave", "anche a me")
	e.say("app", n, "carol", "ancora @bob")
	e.due()
	if got := e.dispatch(); got != 1 {
		t.Fatalf("email partite = %d, voluta 1 (raggruppamento)", got)
	}
	msgs := e.smtp.Messages()
	if len(msgs) != 1 || len(msgs[0].To) != 1 || msgs[0].To[0] != "bob@example.com" {
		t.Fatalf("email = %+v", msgs)
	}
	body := msgs[0].Data
	for _, want := range []string{"Auto-Submitted: auto-generated", "Subject: [alice/app] Crash"} {
		if !strings.Contains(body, want) {
			t.Errorf("manca %q:\n%s", want, body)
		}
	}
	// Il corpo è quoted-printable: il link con "=" a capo non si confonde, lo
	// si cerca senza i soft line break.
	flat := strings.ReplaceAll(body, "=\r\n", "")
	if !strings.Contains(flat, fmt.Sprintf("https://git.example.com/alice/app/issues/%d", n)) {
		t.Errorf("manca il link alla issue:\n%s", body)
	}
	if strings.Count(flat, "carol ha commentato") != 2 {
		t.Errorf("attese due menzioni nella stessa email:\n%s", body)
	}
	if strings.Contains(strings.ToLower(body), "reply-to") {
		t.Error("nessuna risposta via email")
	}
	if c := e.count(`SELECT count(*) FROM core.notifications WHERE user_id = $1 AND email_sent_at IS NOT NULL`, bobID); c != 2 {
		t.Errorf("notifiche segnate inviate = %d, volute 2", c)
	}
	if c := e.count(`SELECT count(*) FROM core.notifications WHERE user_id = $1 AND reason = 'participating' AND email_due_at IS NULL AND email_sent_at IS NULL`, bobID); c != 1 {
		t.Errorf("il commento di dave non doveva avere email (c = %d)", c)
	}
	// Un secondo giro non rimanda niente.
	if got := e.dispatch(); got != 0 || len(e.smtp.Messages()) != 1 {
		t.Fatalf("secondo giro: %d email, %d messaggi", got, len(e.smtp.Messages()))
	}

	// La finestra si riapre: una nuova menzione è un'altra email.
	e.say("app", n, "carol", "e poi @bob")
	e.due()
	if got := e.dispatch(); got != 1 || len(e.smtp.Messages()) != 2 {
		t.Fatalf("nuova finestra: %d email, %d messaggi", got, len(e.smtp.Messages()))
	}

	// Preferenze per tipo: bob spegne le menzioni e accende i commenti.
	p := e.prefs("bob")
	if !p.EmailAvailable || len(p.Email) != 8 || !p.Email["mentioned"] || !p.Email["assigned"] || p.Email["participating"] {
		t.Fatalf("preferenze di default = %+v", p)
	}
	e.want(e.do(http.MethodPut, "/user/notification-preferences", "bob", `{"email":{"mentioned":false,"participating":true}}`), http.StatusOK, "")
	p = e.prefs("bob")
	if p.Email["mentioned"] || !p.Email["participating"] || !p.Email["assigned"] {
		t.Fatalf("dopo il PUT = %+v (i tipi omessi non cambiano)", p)
	}
	e.say("app", n, "carol", "un'altra @bob menzione")
	e.say("app", n, "dave", "un commento")
	e.due()
	if got := e.dispatch(); got != 1 || len(e.smtp.Messages()) != 3 {
		t.Fatalf("dopo le preferenze: %d email, %d messaggi", got, len(e.smtp.Messages()))
	}
	last := strings.ReplaceAll(e.smtp.Messages()[2].Data, "=\r\n", "")
	if strings.Count(last, "dave ha commentato") != 1 || strings.Contains(last, "carol ha commentato") {
		t.Errorf("la menzione spenta non doveva comparire, il commento sì:\n%s", last)
	}
}

func TestEmail_AssegnazioneDiDefaultENonSePrimaLetta(t *testing.T) {
	e := newEmailEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	e.id.grantWrite(repoID, "dave")
	n := e.openWith("app", "bob", "Da assegnare", "testo")
	e.clear()
	p := fmt.Sprintf("/repos/alice/app/issues/%d", n)

	e.want(e.do(http.MethodPut, p+"/assignees", "alice", `{"assignees":["dave"]}`), http.StatusOK, "")
	e.due()
	if got := e.dispatch(); got != 1 || e.smtp.Messages()[0].To[0] != "dave@example.com" {
		t.Fatalf("assegnazione: %d email, %+v", got, e.smtp.Messages())
	}

	// Letta prima dell'invio: l'email non serve più.
	e.want(e.do(http.MethodPut, p+"/assignees", "alice", `{"assignees":["bob"]}`), http.StatusOK, "")
	e.process()
	e.sql(`UPDATE core.notifications SET read_at = now() WHERE user_id = $1`, bobID)
	e.due()
	if got := e.dispatch(); got != 0 || len(e.smtp.Messages()) != 1 {
		t.Fatalf("notifica già letta: %d email, %d messaggi", got, len(e.smtp.Messages()))
	}
	if c := e.count(`SELECT count(*) FROM core.notifications WHERE email_due_at IS NOT NULL AND email_sent_at IS NULL`); c != 0 {
		t.Fatalf("email rimaste in coda: %d", c)
	}
}

func TestEmail_MaiAgliAgentiC4C5(t *testing.T) {
	e := newEmailEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bot")
	e.id.grantWrite(repoID, "bob")
	n := e.openWith("app", "bob", "Per il bot", "ciao @bot")
	e.want(e.do(http.MethodPut, fmt.Sprintf("/repos/alice/app/issues/%d/assignees", n), "bob", `{"assignees":["bot"]}`), http.StatusOK, "")
	e.due()
	// Le notifiche in-app del bot ci sono (C4), ma nessuna email parte.
	if c := e.count(`SELECT count(*) FROM core.notifications WHERE user_id = $1`, botID); c != 2 {
		t.Fatalf("notifiche del bot = %d, volute 2", c)
	}
	if got := e.dispatch(); got != 0 || e.smtp.Attempts() != 0 {
		t.Fatalf("email a un agente: %d partite, %d consegne", got, e.smtp.Attempts())
	}
	if c := e.count(`SELECT count(*) FROM core.notifications WHERE user_id = $1 AND (email_due_at IS NOT NULL OR email_sent_at IS NOT NULL)`, botID); c != 0 {
		t.Fatalf("il bot ha email in coda: %d", c)
	}
	// Le preferenze si leggono e si salvano, ma emailAvailable è false.
	if p := e.prefs("bot"); p.EmailAvailable || len(p.Email) != 8 {
		t.Fatalf("preferenze del bot = %+v", p)
	}
	e.want(e.do(http.MethodPut, "/user/notification-preferences", "bot", `{"email":{"participating":true}}`), http.StatusOK, "")
}

func TestEmail_SenzaSmtpNessunTentativoNessunErrore(t *testing.T) {
	e := newNotifyEnv(t) // router senza WithEmail, motore con EmailWindow 0
	srv := mailertest.Start(t, &mailertest.Server{})
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	n := e.openWith("app", "bob", "Senza SMTP", "ciao @carol")
	e.say("app", n, "carol", "ehi @bob")
	e.want(e.do(http.MethodPut, fmt.Sprintf("/repos/alice/app/issues/%d/assignees", n), "alice", `{"assignees":["bob"]}`), http.StatusOK, "")
	e.process()

	var total, queued int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*), count(email_due_at) FROM core.notifications`).Scan(&total, &queued); err != nil {
		t.Fatal(err)
	}
	if total < 2 || queued != 0 {
		t.Fatalf("notifiche = %d, in coda per email = %d (volute >= 2 e 0)", total, queued)
	}
	if srv.Attempts() != 0 {
		t.Fatalf("tentativi di invio senza SMTP: %d", srv.Attempts())
	}
	// La UI nasconde le opzioni: emailAvailable è false, le preferenze si salvano.
	rec := e.do(http.MethodGet, "/user/notification-preferences", "bob", "")
	e.want(rec, http.StatusOK, "")
	if p := decodeInto[openapi.NotificationPreferences](t, rec); p.EmailAvailable || len(p.Email) != 8 || !p.Email["mentioned"] {
		t.Fatalf("preferenze senza SMTP = %+v", p)
	}
	e.want(e.do(http.MethodPut, "/user/notification-preferences", "bob", `{"email":{"mentioned":false}}`), http.StatusOK, "")
}

func TestEmail_ErroriSmtpTentativiLimitatiENotificheInAppIntatte(t *testing.T) {
	e := newEmailEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	n := e.openWith("app", "bob", "SMTP rotto", "testo")
	e.clear()

	// Un errore passeggero: la seconda volta riesce, una sola email.
	e.smtp.FailNext(1)
	e.say("app", n, "carol", "primo @bob")
	e.due()
	if got := e.dispatch(); got != 0 {
		t.Fatalf("il primo invio doveva fallire, partite %d", got)
	}
	if c := e.count(`SELECT email_attempts FROM core.notifications WHERE user_id = $1`, bobID); c != 1 {
		t.Fatalf("tentativi = %d, voluto 1", c)
	}
	time.Sleep(20 * time.Millisecond) // l'attesa è di 1 ms
	if got := e.dispatch(); got != 1 || len(e.smtp.Messages()) != 1 {
		t.Fatalf("secondo tentativo: %d email, %d messaggi", got, len(e.smtp.Messages()))
	}

	// SMTP sempre giù: al massimo MaxAttempts tentativi, poi si smette.
	e.smtp.FailNext(-1)
	before := e.smtp.Attempts()
	e.say("app", n, "carol", "secondo @bob")
	e.due()
	for i := 0; i < 8; i++ {
		e.dispatch()
		time.Sleep(20 * time.Millisecond)
		e.sql(`UPDATE core.notifications SET email_due_at = clock_timestamp() - interval '1 second' WHERE email_due_at IS NOT NULL AND email_sent_at IS NULL`)
	}
	if got := e.smtp.Attempts() - before; got != 3 {
		t.Fatalf("tentativi di invio = %d, voluti 3 (MaxAttempts)", got)
	}
	if c := e.count(`SELECT count(*) FROM core.notifications WHERE user_id = $1 AND email_failed_at IS NOT NULL AND email_sent_at IS NULL AND email_due_at IS NULL`, bobID); c != 1 {
		t.Fatalf("notifiche con invio fallito = %d, voluta 1", c)
	}
	// Le notifiche in-app non ne risentono.
	if l := e.inbox("bob", ""); l.Total != 2 || l.UnreadCount != 2 {
		t.Fatalf("casella di bob = %+v", l)
	}
}

func TestEmail_PreferenzeValidazione(t *testing.T) {
	e := newEmailEnv(t)
	rec := e.do(http.MethodPut, "/user/notification-preferences", "bob", `{"email":{"mentioned":false,"boh":true}}`)
	e.want(rec, http.StatusUnprocessableEntity, "validation_failed")
	if !strings.Contains(rec.Body.String(), `"fields"`) || !strings.Contains(rec.Body.String(), `"email"`) {
		t.Fatalf("manca details.fields.email: %s", rec.Body.String())
	}
	if p := e.prefs("bob"); !p.Email["mentioned"] {
		t.Fatalf("un 422 non deve cambiare niente: %+v", p)
	}
	e.want(e.do(http.MethodPut, "/user/notification-preferences", "bob", `{"altro":1}`), http.StatusBadRequest, "")
	e.want(e.do(http.MethodPut, "/user/notification-preferences", "bob", `{}`), http.StatusBadRequest, "")
	// Le preferenze sono dell'utente corrente: alice non vede quelle di bob.
	e.want(e.do(http.MethodPut, "/user/notification-preferences", "bob", `{"email":{"state_change":true}}`), http.StatusOK, "")
	if p := e.prefs("alice"); p.Email["state_change"] {
		t.Fatal("le preferenze di bob non valgono per alice")
	}
}
