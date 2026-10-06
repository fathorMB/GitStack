// Package notify è il motore delle notifiche in-app di core (M-06/E,
// GIT-133; regole C3, C4, C9 e I8 in
// .prisma/knowledge/topics/collegamenti-notifiche-webhook.md e issues.md).
//
// Come funziona: gli eventi di dominio (issue.*, issue_comment.*) sono già
// nell'outbox transazionale (M-06/B, 0009), scritti nella transazione della
// modifica. Il motore li legge da lì, non da NATS, e scrive core.notifications
// nella STESSA transazione in cui segna l'evento come elaborato
// (core.event_outbox.notified_at, 0010): se la scrittura fallisce l'evento
// resta da fare, se riesce non si rielabora, quindi nessuna notifica persa o
// doppia anche con più repliche (un advisory lock serializza i giri) e anche
// con NATS fermo. La richiesta HTTP che cambia la issue non aspetta il motore
// e non dipende da identity per notificare.
//
// Chi riceve cosa lo decide questo pacchetto; chi vede il repo lo decide
// identity (HasRole read): nessuna notifica, nemmeno di titolo o motivo, a chi
// non legge il repo (I8, C9).
package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/domainevents"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Motivi delle notifiche (NotificationReason del contratto).
const (
	ReasonAssigned      = "assigned"
	ReasonMentioned     = "mentioned"
	ReasonParticipating = "participating"
	ReasonSubscribed    = "subscribed"
	// ReasonCommitLinked: un commit cita una issue seguita (C2). Lo produce
	// l'item dei collegamenti (M-06/C, GIT-131) con Engine.Notify; qui nessun
	// produttore.
	ReasonCommitLinked = "commit_linked"
	ReasonStateChange  = "state_change"
	ReasonWebhook      = "webhook"
)

// priority: se un utente ha più motivi per lo stesso evento riceve una sola
// notifica, con il motivo più forte.
var priority = map[string]int{
	ReasonAssigned: 6, ReasonMentioned: 5, ReasonCommitLinked: 4, ReasonStateChange: 3, ReasonParticipating: 2, ReasonSubscribed: 1,
}

// Identity è ciò che il motore chiede a identity.
type Identity interface {
	// HasRole dice se l'utente ha almeno il ruolo (read|write|admin) sul repo.
	HasRole(ctx context.Context, userID, resourceID uuid.UUID, role string) (bool, error)
	// ResolveMentions risolve `utente` e `org/team` (I8).
	ResolveMentions(ctx context.Context, names []string) (identityclient.Mentions, error)
}

// Engine elabora gli eventi dell'outbox e conserva le notifiche.
type Engine struct {
	Pool     *pgxpool.Pool
	Identity Identity
	Log      *slog.Logger
	// Now è l'orologio (iniettabile nei test); default time.Now.
	Now func() time.Time

	Poll  time.Duration // default 1s
	Batch int           // default 50
	// ReadRetention: le lette si eliminano dopo (C9); default 90 giorni.
	ReadRetention time.Duration
	// PurgeEvery: ogni quanto Run lancia PurgeRead; default 1 ora.
	PurgeEvery time.Duration
}

// DefaultReadRetention è la conservazione delle notifiche lette (C9).
const DefaultReadRetention = 90 * 24 * time.Hour

// advisoryKey serializza i giri del motore fra le repliche.
const advisoryKey int64 = 0x6e6f74696679 // "notify"

func (e *Engine) defaults() {
	if e.Poll <= 0 {
		e.Poll = time.Second
	}
	if e.Batch <= 0 {
		e.Batch = 50
	}
	if e.ReadRetention <= 0 {
		e.ReadRetention = DefaultReadRetention
	}
	if e.PurgeEvery <= 0 {
		e.PurgeEvery = time.Hour
	}
	if e.Log == nil {
		e.Log = slog.Default()
	}
	if e.Now == nil {
		e.Now = time.Now
	}
}

// Run elabora gli eventi e fa la conservazione finché ctx non finisce.
func (e *Engine) Run(ctx context.Context) {
	e.defaults()
	t := time.NewTicker(e.Poll)
	defer t.Stop()
	var lastPurge time.Time
	for {
		for {
			n, err := e.Process(ctx)
			if err != nil {
				if ctx.Err() == nil {
					e.Log.Warn("notifiche: elaborazione degli eventi non riuscita, si ritenta", "err", err)
				}
				break
			}
			if n < e.Batch {
				break
			}
		}
		if now := e.Now(); lastPurge.IsZero() || now.Sub(lastPurge) >= e.PurgeEvery {
			lastPurge = now
			if n, err := e.PurgeRead(ctx, now); err != nil {
				if ctx.Err() == nil {
					e.Log.Warn("notifiche: conservazione non riuscita", "err", err)
				}
			} else if n > 0 {
				e.Log.Info("notifiche: lette eliminate dopo la conservazione", "count", n)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// PurgeRead elimina le notifiche lette prima di now - ReadRetention (C9: 90
// giorni; le non lette restano). Idempotente: un secondo giro non trova
// niente. now è iniettato per i test.
func (e *Engine) PurgeRead(ctx context.Context, now time.Time) (int64, error) {
	e.defaults()
	tag, err := e.Pool.Exec(ctx, `DELETE FROM core.notifications WHERE read_at IS NOT NULL AND read_at < $1`,
		now.Add(-e.ReadRetention))
	if err != nil {
		return 0, fmt.Errorf("eliminazione delle notifiche lette: %w", err)
	}
	return tag.RowsAffected(), nil
}

type outboxEvent struct {
	seq     int64
	id      uuid.UUID
	name    string
	payload []byte
}

// Process elabora un lotto di eventi non ancora letti e ritorna quanti ne ha
// segnati. Un errore (identity irraggiungibile, database) annulla tutto il
// lotto: gli eventi restano da fare e il prossimo giro li riprende.
func (e *Engine) Process(ctx context.Context) (int, error) {
	e.defaults()
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var got bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, advisoryKey).Scan(&got); err != nil {
		return 0, err
	}
	if !got {
		return 0, nil // un'altra replica sta elaborando
	}
	rows, err := tx.Query(ctx, `SELECT seq, id, name, payload FROM core.event_outbox
		WHERE notified_at IS NULL ORDER BY seq LIMIT $1`, e.Batch)
	if err != nil {
		return 0, err
	}
	var batch []outboxEvent
	for rows.Next() {
		var ev outboxEvent
		if err := rows.Scan(&ev.seq, &ev.id, &ev.name, &ev.payload); err != nil {
			rows.Close()
			return 0, err
		}
		batch = append(batch, ev)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(batch) == 0 {
		return 0, nil
	}
	seqs := make([]int64, 0, len(batch))
	for _, ev := range batch {
		if err := e.handle(ctx, tx, ev); err != nil {
			var bad *badEventError
			if !errors.As(err, &bad) {
				return 0, fmt.Errorf("evento %s (%s): %w", ev.name, ev.id, err)
			}
			// Un evento illeggibile non si ritenta mai: si segna e si va avanti.
			e.Log.Error("notifiche: evento non valido, ignorato", "event", ev.name, "id", ev.id, "err", err)
		}
		seqs = append(seqs, ev.seq)
	}
	if _, err := tx.Exec(ctx, `UPDATE core.event_outbox SET notified_at = clock_timestamp() WHERE seq = ANY($1)`, seqs); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(batch), nil
}

type badEventError struct{ err error }

func (b *badEventError) Error() string { return b.err.Error() }
func (b *badEventError) Unwrap() error { return b.err }

// occurrence è un evento ridotto a ciò che serve a decidere le notifiche.
type occurrence struct {
	name    string
	repoID  uuid.UUID
	issueID uuid.UUID
	// actor è chi ha agito; uuid.Nil = sistema (nessuno da escludere). Vale
	// anche per le azioni fatte con un token personale: l'attore è sempre
	// l'utente del token (C3).
	actor    uuid.UUID
	authorID uuid.UUID
	hidden   bool
	// assignee è il destinatario di issue.assigned.
	assignee  uuid.UUID
	commentID *uuid.UUID
	// text e prevText: il testo che può contenere menzioni e quello di prima
	// (le modifiche notificano solo i menzionati nuovi).
	text, prevText string
	data           map[string]any
	// access ricorda chi legge il repo (risposte di identity per questo evento).
	access map[uuid.UUID]bool
}

func (e *Engine) handle(ctx context.Context, tx pgx.Tx, ev outboxEvent) error {
	switch ev.name {
	case domainevents.IssueCreated, domainevents.IssueEdited, domainevents.IssueClosed,
		domainevents.IssueReopened, domainevents.IssueAssigned:
		var p domainevents.IssuePayload
		if err := json.Unmarshal(ev.payload, &p); err != nil {
			return &badEventError{err}
		}
		o, err := occurrenceOf(ev.name, p.Repo, p.Actor, p.Issue)
		if err != nil {
			return &badEventError{err}
		}
		o.data = map[string]any{"number": p.Issue.Number, "title": p.Issue.Title, "state": p.Issue.State}
		switch ev.name {
		case domainevents.IssueCreated:
			if err := tx.QueryRow(ctx, `SELECT body FROM core.issues WHERE id = $1`, o.issueID).Scan(&o.text); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		case domainevents.IssueEdited:
			if p.Changes == nil || p.Changes.Body == nil {
				return nil // il titolo non cita nessuno
			}
			if err := tx.QueryRow(ctx, `SELECT body FROM core.issues WHERE id = $1`, o.issueID).Scan(&o.text); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			o.prevText = p.Changes.Body.From
		case domainevents.IssueClosed:
			if p.Reason != "" {
				o.data["closeReason"] = p.Reason
			}
		case domainevents.IssueAssigned:
			if p.Assignee == nil {
				return &badEventError{errors.New("issue.assigned senza assegnatario")}
			}
			if o.assignee, err = uuid.Parse(p.Assignee.ID); err != nil {
				return &badEventError{err}
			}
		}
		return e.deliver(ctx, tx, o)
	case domainevents.IssueCommentCreated, domainevents.IssueCommentEdited:
		var p domainevents.IssueCommentPayload
		if err := json.Unmarshal(ev.payload, &p); err != nil {
			return &badEventError{err}
		}
		o, err := occurrenceOf(ev.name, p.Repo, p.Actor, p.Issue)
		if err != nil {
			return &badEventError{err}
		}
		cid, err := uuid.Parse(p.Comment.ID)
		if err != nil {
			return &badEventError{err}
		}
		o.commentID = &cid
		o.text = p.Comment.Body
		if p.Changes != nil && p.Changes.Body != nil {
			o.prevText = p.Changes.Body.From
		}
		o.data = map[string]any{"number": p.Issue.Number, "title": p.Issue.Title, "state": p.Issue.State}
		return e.deliver(ctx, tx, o)
	}
	return nil
}

func occurrenceOf(name string, repo domainevents.Repo, actor *domainevents.User, iss domainevents.Issue) (occurrence, error) {
	o := occurrence{name: name, hidden: iss.Hidden}
	var err error
	if o.repoID, err = uuid.Parse(repo.ID); err != nil {
		return o, err
	}
	if o.issueID, err = uuid.Parse(iss.ID); err != nil {
		return o, err
	}
	if o.authorID, err = uuid.Parse(iss.AuthorID); err != nil {
		return o, err
	}
	if actor != nil {
		if o.actor, err = uuid.Parse(actor.ID); err != nil {
			return o, err
		}
	}
	return o, nil
}

// Notify è il punto d'ingresso per i produttori che non passano da un evento
// issue/issue_comment del motore (oggi nessuno: i commit collegati,
// ReasonCommitLinked, arriveranno da M-06/C). Notifica i destinatari
// indicati, già filtrati per Watch Ignore e visibilità del repo.
func (e *Engine) Notify(ctx context.Context, tx pgx.Tx, reason string, recipients []uuid.UUID, o OccurrenceInput) error {
	e.defaults()
	oc := occurrence{name: o.EventName, repoID: o.RepoID, issueID: o.IssueID, actor: o.Actor, hidden: o.Hidden, data: o.Data}
	cands := map[uuid.UUID]string{}
	for _, r := range recipients {
		if r != oc.actor {
			cands[r] = reason
		}
	}
	return e.insert(ctx, tx, oc, cands)
}

// OccurrenceInput descrive un evento di un produttore esterno al motore.
type OccurrenceInput struct {
	EventName string
	RepoID    uuid.UUID
	IssueID   uuid.UUID
	Actor     uuid.UUID // uuid.Nil = sistema
	Hidden    bool
	Data      map[string]any
}

// deliver decide i destinatari di un evento e scrive le notifiche.
func (e *Engine) deliver(ctx context.Context, tx pgx.Tx, o occurrence) error {
	o.access = map[uuid.UUID]bool{}
	cands := map[uuid.UUID]string{}
	add := func(u uuid.UUID, reason string) {
		if u == uuid.Nil || u == o.actor { // C3: mai per le proprie azioni
			return
		}
		if priority[reason] > priority[cands[u]] {
			cands[u] = reason
		}
	}
	autoSub := func(u uuid.UUID, reason string) error {
		// ON CONFLICT DO NOTHING: l'iscrizione automatica non sovrascrive
		// un'iscrizione esistente, in particolare un Unsubscribe esplicito.
		_, err := tx.Exec(ctx, `INSERT INTO core.issue_subscriptions (issue_id, user_id, reason)
			SELECT id, $2, $3 FROM core.issues WHERE id = $1
			ON CONFLICT (issue_id, user_id) DO NOTHING`, o.issueID, u, reason)
		return err
	}

	// Partecipanti automatici.
	switch o.name {
	case domainevents.IssueCreated:
		if err := autoSub(o.authorID, "author"); err != nil {
			return err
		}
	case domainevents.IssueCommentCreated:
		if o.actor != uuid.Nil {
			if err := autoSub(o.actor, "commenter"); err != nil {
				return err
			}
		}
	case domainevents.IssueAssigned:
		if err := autoSub(o.assignee, "assignee"); err != nil {
			return err
		}
		add(o.assignee, ReasonAssigned) // anche dopo un Unsubscribe
	}

	// Menzioni (I8): utenti, agenti e team espansi nei membri.
	mentioned, err := e.mentioned(ctx, newMentions(o.text, o.prevText))
	if err != nil {
		return err
	}
	for _, u := range mentioned {
		// Chi non vede il repo non viene nemmeno iscritto: resta testo (I8).
		if ok, err := e.canSee(ctx, &o, u); err != nil {
			return err
		} else if !ok {
			continue
		}
		if err := autoSub(u, "mentioned"); err != nil {
			return err
		}
		add(u, ReasonMentioned) // anche dopo un Unsubscribe
	}

	// Iscritti e Watch del repo.
	switch o.name {
	case domainevents.IssueCreated, domainevents.IssueCommentCreated:
		if o.name == domainevents.IssueCommentCreated {
			if err := e.addSubscribers(ctx, tx, o, add, false); err != nil {
				return err
			}
		}
		rows, err := tx.Query(ctx, `SELECT user_id FROM core.repo_watches WHERE repo_id = $1 AND mode = 'all'`, o.repoID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var u uuid.UUID
			if err := rows.Scan(&u); err != nil {
				rows.Close()
				return err
			}
			add(u, ReasonSubscribed)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	case domainevents.IssueClosed, domainevents.IssueReopened:
		if err := e.addSubscribers(ctx, tx, o, add, true); err != nil {
			return err
		}
	}
	return e.insert(ctx, tx, o, cands)
}

// addSubscribers aggiunge chi segue la issue: con stateChange il motivo è
// state_change, altrimenti participating per chi è coinvolto (autore,
// assegnatario, commentatore, menzionato) e subscribed per Subscribe.
func (e *Engine) addSubscribers(ctx context.Context, tx pgx.Tx, o occurrence, add func(uuid.UUID, string), stateChange bool) error {
	rows, err := tx.Query(ctx, `SELECT user_id, reason FROM core.issue_subscriptions WHERE issue_id = $1 AND subscribed`, o.issueID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var u uuid.UUID
		var reason string
		if err := rows.Scan(&u, &reason); err != nil {
			return err
		}
		switch {
		case stateChange:
			add(u, ReasonStateChange)
		case reason == "manual":
			add(u, ReasonSubscribed)
		default:
			add(u, ReasonParticipating)
		}
	}
	return rows.Err()
}

// mentioned risolve i nomi in utenti attivi; un team si espande nei membri.
func (e *Engine) mentioned(ctx context.Context, names []string) ([]uuid.UUID, error) {
	if len(names) == 0 {
		return nil, nil
	}
	res, err := e.Identity.ResolveMentions(ctx, names)
	if err != nil {
		return nil, fmt.Errorf("risoluzione delle menzioni: %w", err)
	}
	seen := map[uuid.UUID]bool{}
	var out []uuid.UUID
	push := func(u identityclient.CodeUser) {
		if !seen[u.ID] {
			seen[u.ID] = true
			out = append(out, u.ID)
		}
	}
	for _, n := range names {
		if u, ok := res.Users[n]; ok {
			push(u)
		}
		for _, m := range res.Teams[n] {
			push(m)
		}
	}
	return out, nil
}

// canSee dice se l'utente legge il repo dell'evento (admin, se la issue è
// nascosta); la risposta di identity si ricorda per tutto l'evento.
func (e *Engine) canSee(ctx context.Context, o *occurrence, u uuid.UUID) (bool, error) {
	if ok, done := o.access[u]; done {
		return ok, nil
	}
	role := "read"
	if o.hidden {
		role = "admin"
	}
	ok, err := e.Identity.HasRole(ctx, u, o.repoID, role)
	if err != nil {
		return false, fmt.Errorf("verifica dell'accesso al repo: %w", err)
	}
	if o.access == nil {
		o.access = map[uuid.UUID]bool{}
	}
	o.access[u] = ok
	return ok, nil
}

// insert applica Watch Ignore e visibilità e scrive le notifiche.
func (e *Engine) insert(ctx context.Context, tx pgx.Tx, o occurrence, cands map[uuid.UUID]string) error {
	if len(cands) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(cands))
	for u := range cands {
		ids = append(ids, u)
	}
	// Watch Ignore: nessuna notifica dal repo, nemmeno le menzioni.
	rows, err := tx.Query(ctx, `SELECT user_id FROM core.repo_watches WHERE repo_id = $1 AND mode = 'ignore' AND user_id = ANY($2)`, o.repoID, ids)
	if err != nil {
		return err
	}
	for rows.Next() {
		var u uuid.UUID
		if err := rows.Scan(&u); err != nil {
			rows.Close()
			return err
		}
		delete(cands, u)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	ordered := make([]uuid.UUID, 0, len(cands))
	for u := range cands {
		ordered = append(ordered, u)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].String() < ordered[j].String() })
	var actor *uuid.UUID
	if o.actor != uuid.Nil {
		actor = &o.actor
	}
	data, err := json.Marshal(o.data)
	if err != nil {
		return err
	}
	for _, u := range ordered {
		// I8, C9: solo chi legge il repo (una issue nascosta, solo chi è admin).
		ok, err := e.canSee(ctx, &o, u)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		var issueID *uuid.UUID
		if o.issueID != uuid.Nil {
			issueID = &o.issueID
		}
		// Se il repo o la issue sono spariti nel frattempo (eliminazione
		// definitiva) non c'è nulla da notificare.
		if _, err := tx.Exec(ctx, `INSERT INTO core.notifications
				(id, user_id, reason, repo_id, issue_id, comment_id, actor_id, event_name, data, created_at)
			SELECT $1, $2, $3, r.resource_id, i.id, c.id, $7, $8, $9, clock_timestamp()
			FROM core.repositories r
			LEFT JOIN core.issues i ON i.id = $5 AND i.repo_id = r.resource_id
			LEFT JOIN core.issue_comments c ON c.id = $6
			WHERE r.resource_id = $4 AND ($5::uuid IS NULL OR i.id IS NOT NULL)`,
			uuid.New(), u, cands[u], o.repoID, issueID, o.commentID, actor, o.name, data); err != nil {
			return fmt.Errorf("scrittura della notifica: %w", err)
		}
	}
	return nil
}
