package notify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/domainevents"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/mailer"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Email delle notifiche (M-06/F, GIT-134, regola C5).
//
// Il motore (Engine.insert) scrive la notifica in-app e, se l'SMTP è attivo e
// l'utente vuole l'email per quel tipo, ne fissa l'orario di invio
// (email_due_at). Questo dispatcher gira a parte, in un'altra goroutine e con
// altre connessioni: un SMTP lento, rotto o spento non rallenta mai la
// transazione del motore né le notifiche in-app.
//
// Raggruppamento: la prima notifica per email di un utente su una issue fissa
// l'invio a EmailWindow dopo (default 10 secondi, DefaultEmailWindow); le
// notifiche per la stessa issue che arrivano nel frattempo ne ereditano
// l'orario e partono nella stessa email. Poi la finestra si riapre.
//
// Errori: un invio fallito si ritenta con attesa crescente (Backoff, 2×Backoff,
// …) fino a MaxAttempts tentativi in tutto; dopo l'ultimo la notifica
// resta solo in-app (email_failed_at) e si scrive un log di errore. Un errore
// di identity (mancano email e tipo) non consuma tentativi: si riprova al giro
// dopo. Mai email a un agente (C4, C5), a chi non ha un indirizzo o ha già
// letto o archiviato la notifica prima dell'invio.

// DefaultEmailWindow è la finestra di raggruppamento delle email per issue.
const DefaultEmailWindow = 10 * time.Second

// DefaultEmailAttempts è il numero massimo di tentativi di invio.
const DefaultEmailAttempts = 3

// emailAdvisoryKey serializza i giri del dispatcher fra le repliche.
const emailAdvisoryKey int64 = 0x6e6f74656d6c // "notemail"

// Users risolve gli id utente in nome, tipo ed email (identity).
type Users interface {
	LookupUsers(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]identityclient.CodeUser, error)
}

// Mail è ciò che serve per spedire un'email (mailer.Mailer).
type Mail interface {
	Send(ctx context.Context, msg mailer.Message) error
}

// EmailDispatcher manda le email in coda. Senza Mail (SMTP spento) non si
// usa: Run non va nemmeno avviato.
type EmailDispatcher struct {
	Pool  *pgxpool.Pool
	Users Users
	Mail  Mail
	// PublicURL è la base dei link alle issue (GITSTACK_CORE_PUBLIC_URL).
	PublicURL string
	Log       *slog.Logger
	Now       func() time.Time

	Poll        time.Duration // default 2s
	MaxAttempts int           // tentativi in tutto, default DefaultEmailAttempts
	Backoff     time.Duration // attesa dopo il primo errore, default 1 min
}

func (d *EmailDispatcher) defaults() {
	if d.Poll <= 0 {
		d.Poll = 2 * time.Second
	}
	if d.MaxAttempts <= 0 {
		d.MaxAttempts = DefaultEmailAttempts
	}
	if d.Backoff <= 0 {
		d.Backoff = time.Minute
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.Now == nil {
		d.Now = time.Now
	}
}

// Run spedisce le email dovute finché ctx non finisce.
func (d *EmailDispatcher) Run(ctx context.Context) {
	d.defaults()
	t := time.NewTicker(d.Poll)
	defer t.Stop()
	for {
		if _, err := d.Process(ctx); err != nil && ctx.Err() == nil {
			d.Log.Warn("email: giro non riuscito, si ritenta", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

type emailRow struct {
	id        uuid.UUID
	userID    uuid.UUID
	issueID   *uuid.UUID
	reason    string
	actorID   *uuid.UUID
	eventName string
	createdAt time.Time
	attempts  int
	owner     *string
	repo      *string
	number    *int64
	title     *string
	groupKey  uuid.UUID
}

// Process spedisce le email dovute ora e ritorna quante ne ha consegnate al
// server SMTP. Un solo dispatcher alla volta fra le repliche.
func (d *EmailDispatcher) Process(ctx context.Context) (int, error) {
	d.defaults()
	conn, err := d.Pool.Acquire(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Release()
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, emailAdvisoryKey).Scan(&got); err != nil {
		return 0, err
	}
	if !got {
		return 0, nil
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, emailAdvisoryKey)
	}()

	// Già lette o archiviate: l'email non serve più.
	if _, err := conn.Exec(ctx, `UPDATE core.notifications SET email_due_at = NULL
		WHERE email_due_at IS NOT NULL AND email_sent_at IS NULL AND email_due_at <= clock_timestamp()
		AND (read_at IS NOT NULL OR archived_at IS NOT NULL)`); err != nil {
		return 0, err
	}
	rows, err := conn.Query(ctx, `SELECT n.id, n.user_id, n.issue_id, n.reason, n.actor_id, n.event_name, n.created_at,
			n.email_attempts, r.owner_name, r.name, i.number, i.title, COALESCE(n.issue_id, n.id)
		FROM core.notifications n
		LEFT JOIN core.repositories r ON r.resource_id = n.repo_id
		LEFT JOIN core.issues i ON i.id = n.issue_id
		WHERE n.email_due_at IS NOT NULL AND n.email_due_at <= clock_timestamp() AND n.email_sent_at IS NULL
		ORDER BY n.user_id, COALESCE(n.issue_id, n.id), n.created_at, n.id
		LIMIT 500`)
	if err != nil {
		return 0, err
	}
	var due []emailRow
	for rows.Next() {
		var r emailRow
		if err := rows.Scan(&r.id, &r.userID, &r.issueID, &r.reason, &r.actorID, &r.eventName, &r.createdAt,
			&r.attempts, &r.owner, &r.repo, &r.number, &r.title, &r.groupKey); err != nil {
			rows.Close()
			return 0, err
		}
		due = append(due, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(due) == 0 {
		return 0, nil
	}

	// Una sola ricerca in identity per destinatari e autori.
	seen := map[uuid.UUID]bool{}
	var ids []uuid.UUID
	for _, r := range due {
		for _, id := range []*uuid.UUID{&r.userID, r.actorID} {
			if id != nil && !seen[*id] {
				seen[*id] = true
				ids = append(ids, *id)
			}
		}
	}
	people, err := d.Users.LookupUsers(ctx, ids)
	if err != nil {
		return 0, fmt.Errorf("ricerca dei destinatari: %w", err)
	}

	sent := 0
	for start := 0; start < len(due); {
		end := start + 1
		for end < len(due) && due[end].userID == due[start].userID && due[end].groupKey == due[start].groupKey {
			end++
		}
		group := due[start:end]
		start = end
		ok, err := d.sendGroup(ctx, conn, group, people)
		if err != nil {
			return sent, err
		}
		if ok {
			sent++
		}
	}
	return sent, nil
}

func groupIDs(group []emailRow) []uuid.UUID {
	out := make([]uuid.UUID, len(group))
	for i, r := range group {
		out[i] = r.id
	}
	return out
}

// sendGroup manda un'email per un gruppo (utente, issue). Ritorna true se è
// partita; un errore ritornato è del database (si ferma il giro).
func (d *EmailDispatcher) sendGroup(ctx context.Context, conn *pgxpool.Conn, group []emailRow, people map[uuid.UUID]identityclient.CodeUser) (bool, error) {
	u, found := people[group[0].userID]
	if !found || u.Kind == "agent" || strings.TrimSpace(u.Email) == "" {
		// Nessuna email agli agenti (C4, C5) né a chi non ha un indirizzo.
		_, err := conn.Exec(ctx, `UPDATE core.notifications SET email_due_at = NULL WHERE id = ANY($1)`, groupIDs(group))
		return false, err
	}
	msg := d.compose(u.Email, group, people)
	if err := d.Mail.Send(ctx, msg); err != nil {
		attempts := group[0].attempts + 1
		for _, r := range group {
			attempts = max(attempts, r.attempts+1)
		}
		if attempts >= d.MaxAttempts {
			d.Log.Error("email: invio non riuscito, tentativi esauriti: la notifica resta solo in-app",
				"user", group[0].userID, "notifications", len(group), "attempts", attempts, "err", err)
			_, derr := conn.Exec(ctx, `UPDATE core.notifications SET email_attempts = $2, email_due_at = NULL, email_failed_at = clock_timestamp()
				WHERE id = ANY($1)`, groupIDs(group), attempts)
			return false, derr
		}
		wait := d.Backoff * time.Duration(1<<(attempts-1))
		d.Log.Warn("email: invio non riuscito, si ritenta", "user", group[0].userID, "attempt", attempts,
			"of", d.MaxAttempts, "retry_in", wait.String(), "err", err)
		_, derr := conn.Exec(ctx, `UPDATE core.notifications SET email_attempts = $2, email_due_at = clock_timestamp() + $3::float8 * interval '1 second'
			WHERE id = ANY($1)`, groupIDs(group), attempts, wait.Seconds())
		return false, derr
	}
	if _, err := conn.Exec(ctx, `UPDATE core.notifications SET email_sent_at = clock_timestamp(), email_due_at = NULL WHERE id = ANY($1)`, groupIDs(group)); err != nil {
		return true, err
	}
	return true, nil
}

var reasonText = map[string]string{
	ReasonAssigned:      "ti è stata assegnata",
	ReasonMentioned:     "sei stato menzionato",
	ReasonParticipating: "partecipi alla issue",
	ReasonSubscribed:    "sei iscritto alla issue",
	ReasonCommitLinked:  "un commit la cita",
	ReasonStateChange:   "cambia lo stato di una issue che segui",
	ReasonWebhook:       "un webhook è stato disattivato",
}

var eventText = map[string]string{
	domainevents.IssueCreated:        "ha aperto la issue",
	domainevents.IssueEdited:         "ha modificato la issue",
	domainevents.IssueClosed:         "ha chiuso la issue",
	domainevents.IssueReopened:       "ha riaperto la issue",
	domainevents.IssueAssigned:       "ha assegnato la issue",
	domainevents.IssueCommentCreated: "ha commentato",
	domainevents.IssueCommentEdited:  "ha modificato un commento",
}

// compose scrive l'email di un gruppo: oggetto con repo, numero e titolo,
// una riga per aggiornamento e il link alla issue. Nessun invito a rispondere.
func (d *EmailDispatcher) compose(to string, group []emailRow, people map[uuid.UUID]identityclient.CodeUser) mailer.Message {
	first := group[0]
	base := strings.TrimRight(d.PublicURL, "/")
	var subject, link, head string
	switch {
	case first.owner != nil && first.repo != nil && first.number != nil:
		full := *first.owner + "/" + *first.repo
		title := ""
		if first.title != nil {
			title = *first.title
		}
		subject = fmt.Sprintf("[%s] %s (#%d)", full, title, *first.number)
		link = fmt.Sprintf("%s/%s/issues/%d", base, full, *first.number)
		head = fmt.Sprintf("Novità sulla issue %s#%d «%s»:", full, *first.number, title)
	default:
		subject = "GitStack: nuova notifica"
		link = base + "/notifications"
		head = "Hai una nuova notifica su GitStack:"
	}
	var b strings.Builder
	b.WriteString(head + "\n\n")
	for _, r := range group {
		who := "GitStack"
		if r.actorID != nil {
			if a, ok := people[*r.actorID]; ok {
				who = a.Username
			}
		}
		what := eventText[r.eventName]
		if what == "" {
			what = "ha aggiornato la issue"
		}
		line := fmt.Sprintf("- %s %s (%s)", who, what, reasonText[r.reason])
		if r.eventName == "" {
			line = fmt.Sprintf("- %s", capitalize(reasonText[r.reason]))
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\nApri su GitStack: " + link + "\n")
	b.WriteString("\n--\nQuesta è una notifica automatica: non rispondere a questa email, nessuna risposta viene letta.\n")
	b.WriteString("Scegli per quali tipi di notifica ricevere le email dalle preferenze di notifica del tuo profilo.\n")
	return mailer.Message{To: to, Subject: subject, Body: b.String()}
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
