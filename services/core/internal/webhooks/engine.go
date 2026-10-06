package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/fathorMB/GitStack/pkg/egress"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Doer fa la richiesta in uscita. In produzione è il client di pkg/egress
// (NewEgressClient), l'unico modo di uscire (C8); nei test, un client che
// raggiunge un server finto.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Users risolve gli id utente in nome e tipo (identity) per i campi sender,
// author e assignees dei payload.
type Users interface {
	LookupUsers(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]identityclient.CodeUser, error)
}

// Managers dice chi gestisce un webhook, per la notifica di disattivazione
// (C7): gli owner dell'organizzazione e, per un repo, chi lo ha creato se ha
// ancora admin.
type Managers interface {
	OrgOwners(ctx context.Context, orgID uuid.UUID) ([]uuid.UUID, error)
	HasRole(ctx context.Context, userID, resourceID uuid.UUID, role string) (bool, error)
}

// Engine crea le consegne dagli eventi, le invia e le conserva.
type Engine struct {
	Pool *pgxpool.Pool
	Keys *Keyring
	HTTP Doer
	// Users e Managers possono essere nil (nessuna identity): i nomi utente
	// dei payload restano quelli dell'evento e la notifica di disattivazione
	// va solo a chi ha creato un webhook di repo.
	Users    Users
	Managers Managers
	Log      *slog.Logger
	// Now è l'orologio (iniettabile nei test); default time.Now. Decide le
	// attese fra i tentativi e i 3 giorni di fallimenti.
	Now func() time.Time

	Poll  time.Duration // default 1s
	Batch int           // default 50
	// Lease: per quanto una consegna presa in carico non la prende nessun
	// altro; default 2 minuti (più dei 10 s di timeout).
	Lease time.Duration
	// Workers: invii in parallelo; default 4.
	Workers int
	// PurgeEvery: ogni quanto Run elimina il log scaduto; default 1 ora.
	PurgeEvery time.Duration
}

// advisoryKey serializza i giri di creazione delle consegne fra le repliche.
const advisoryKey int64 = 0x7765626b6f6b // "webkok"

func (e *Engine) defaults() {
	if e.Poll <= 0 {
		e.Poll = time.Second
	}
	if e.Batch <= 0 {
		e.Batch = 50
	}
	if e.Lease <= 0 {
		e.Lease = 2 * time.Minute
	}
	if e.Workers <= 0 {
		e.Workers = 4
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

// Run crea le consegne, le invia e fa la conservazione finché ctx non finisce.
func (e *Engine) Run(ctx context.Context) {
	e.defaults()
	t := time.NewTicker(e.Poll)
	defer t.Stop()
	var lastPurge time.Time
	for {
		for {
			n, err := e.ProcessOutbox(ctx)
			if err != nil {
				if ctx.Err() == nil {
					e.Log.Warn("webhook: elaborazione degli eventi non riuscita, si ritenta", "err", err)
				}
				break
			}
			if n < e.Batch {
				break
			}
		}
		for {
			n, err := e.DeliverDue(ctx)
			if err != nil {
				if ctx.Err() == nil {
					e.Log.Warn("webhook: consegna non riuscita, si ritenta", "err", err)
				}
				break
			}
			if n < e.Batch {
				break
			}
		}
		if now := e.Now(); lastPurge.IsZero() || now.Sub(lastPurge) >= e.PurgeEvery {
			lastPurge = now
			if n, err := e.PurgeLog(ctx, now); err != nil {
				if ctx.Err() == nil {
					e.Log.Warn("webhook: conservazione del log non riuscita", "err", err)
				}
			} else if n > 0 {
				e.Log.Info("webhook: consegne scadute eliminate dal log", "count", n)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// PurgeLog elimina le consegne create prima di now - 30 giorni (C7).
// Idempotente. Le consegne ancora pending non si toccano: finiscono prima.
func (e *Engine) PurgeLog(ctx context.Context, now time.Time) (int64, error) {
	e.defaults()
	tag, err := e.Pool.Exec(ctx, `DELETE FROM core.webhook_deliveries WHERE created_at < $1 AND status <> 'pending'`,
		now.Add(-LogRetention))
	if err != nil {
		return 0, fmt.Errorf("eliminazione del log delle consegne: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ---------------------------------------------------------------------------
// Creazione delle consegne dagli eventi di dominio (outbox)

type outboxEvent struct {
	seq     int64
	id      uuid.UUID
	name    string
	payload []byte
	created time.Time
}

// ProcessOutbox crea le consegne per un lotto di eventi non ancora letti e
// ritorna quanti ne ha segnati. Un errore annulla tutto il lotto: gli eventi
// restano da fare.
func (e *Engine) ProcessOutbox(ctx context.Context) (int, error) {
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
	rows, err := tx.Query(ctx, `SELECT seq, id, name, payload, created_at FROM core.event_outbox
		WHERE webhooked_at IS NULL ORDER BY seq LIMIT $1`, e.Batch)
	if err != nil {
		return 0, err
	}
	var batch []outboxEvent
	for rows.Next() {
		var ev outboxEvent
		if err := rows.Scan(&ev.seq, &ev.id, &ev.name, &ev.payload, &ev.created); err != nil {
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
			e.Log.Error("webhook: evento non valido, ignorato", "event", ev.name, "id", ev.id, "err", err)
		}
		seqs = append(seqs, ev.seq)
	}
	if _, err := tx.Exec(ctx, `UPDATE core.event_outbox SET webhooked_at = clock_timestamp() WHERE seq = ANY($1)`, seqs); err != nil {
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

// target è un webhook che riceve un evento.
type target struct {
	id uuid.UUID
}

// matching trova i webhook attivi che ricevono l'evento di un repo: quelli del
// repo e quelli dell'organizzazione proprietaria (C6).
func matching(ctx context.Context, q pgx.Tx, repoID uuid.UUID, event string) ([]target, error) {
	rows, err := q.Query(ctx, `SELECT w.id FROM core.webhooks w
		LEFT JOIN core.repositories r ON r.resource_id = $1
		WHERE w.active AND $2 = ANY(w.events)
		  AND ((w.scope = 'repo' AND w.repo_id = $1)
		    OR (w.scope = 'org' AND r.owner_type = 'organization' AND w.org_id = r.owner_id))
		ORDER BY w.created_at, w.id`, repoID, event)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.id); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// enqueue scrive una consegna pending per ogni webhook, da fare subito. Un
// evento già consegnato a quel webhook (indice unico) non si duplica.
func (e *Engine) enqueue(ctx context.Context, tx pgx.Tx, targets []target, event, action string, source uuid.UUID, payload []byte) error {
	now := e.Now()
	for _, t := range targets {
		if _, err := tx.Exec(ctx, `INSERT INTO core.webhook_deliveries
				(id, webhook_id, event, action, source_event_id, status, next_attempt_at, payload, created_at)
			VALUES ($1, $2, $3, $4, $5, 'pending', $6, $7, $6)
			ON CONFLICT (webhook_id, source_event_id) WHERE source_event_id IS NOT NULL AND redelivery_of IS NULL DO NOTHING`,
			uuid.New(), t.id, event, action, source, now, payload); err != nil {
			return fmt.Errorf("scrittura della consegna: %w", err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Consegna

// DeliverDue prende in carico le consegne pending scadute e le invia (al più
// Workers in parallelo). Ritorna quante ne ha tentate. Un errore del database
// fa fermare il giro: le consegne prese restano affittate fino alla scadenza
// dell'affitto e poi si riprendono.
func (e *Engine) DeliverDue(ctx context.Context) (int, error) {
	e.defaults()
	now := e.Now()
	rows, err := e.Pool.Query(ctx, `UPDATE core.webhook_deliveries d SET next_attempt_at = $2
		WHERE d.id IN (
			SELECT id FROM core.webhook_deliveries
			WHERE status = 'pending' AND next_attempt_at <= $1
			ORDER BY next_attempt_at, created_at, id LIMIT $3
			FOR UPDATE SKIP LOCKED)
		RETURNING d.id`, now, now.Add(e.Lease), e.Batch)
	if err != nil {
		return 0, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, e.Workers)
	for _, id := range ids {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if err := e.attempt(ctx, id); err != nil && ctx.Err() == nil {
				e.Log.Warn("webhook: tentativo di consegna non registrato, si riprende dopo l'affitto", "delivery", id, "err", err)
			}
		}()
	}
	wg.Wait()
	return len(ids), nil
}

type outcome struct {
	status     string // pending | success | failed | gone
	statusCode *int
	durationMs int
	errText    string
	respHeader map[string]string
	respBody   string
	truncated  bool
	reqHeader  map[string]string
}

func (e *Engine) attempt(ctx context.Context, id uuid.UUID) error {
	var (
		webhookID  uuid.UUID
		url, event string
		payload    []byte
		prev       int
		ct, nonce  []byte
		keyID      *string
	)
	err := e.Pool.QueryRow(ctx, `SELECT d.webhook_id, w.url, d.event, d.payload, d.attempt, w.secret_ciphertext, w.secret_nonce, w.secret_key_id
		FROM core.webhook_deliveries d JOIN core.webhooks w ON w.id = d.webhook_id WHERE d.id = $1 AND d.status = 'pending'`, id).
		Scan(&webhookID, &url, &event, &payload, &prev, &ct, &nonce, &keyID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil // eliminata (webhook cancellato) o già chiusa
	case err != nil:
		return err
	}
	secret := ""
	var secretErr error
	if keyID != nil && len(ct) > 0 {
		secret, secretErr = e.Keys.Open(webhookID, ct, nonce, *keyID)
	}

	attemptNo := prev + 1
	var oc outcome
	if secretErr != nil {
		// Senza il segreto non si firma: non si manda una consegna non firmata.
		oc = outcome{status: "failed", errText: "segreto del webhook non leggibile: " + secretErr.Error()}
		oc.reqHeader = map[string]string{}
	} else {
		oc = e.send(ctx, id, url, event, payload, secret)
	}

	now := e.Now()
	var next *time.Time
	if oc.status == "retry" {
		// Attesa crescente; dopo l'ottavo tentativo la consegna è fallita.
		if d, ok := RetryDelay(attemptNo); ok {
			oc.status = "pending"
			t := now.Add(d)
			next = &t
		} else {
			oc.status = "failed"
		}
	}
	var delivered *time.Time
	if oc.status == "success" {
		delivered = &now
	}
	reqH, _ := json.Marshal(oc.reqHeader)
	respH, _ := json.Marshal(oc.respHeader)
	if oc.respHeader == nil {
		respH = []byte("{}")
	}
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `UPDATE core.webhook_deliveries SET status = $2, attempt = $3, next_attempt_at = $4,
			status_code = $5, duration_ms = $6, error = $7, request_headers = $8, response_headers = $9,
			response_body = $10, response_truncated = $11, delivered_at = COALESCE($12, delivered_at)
		WHERE id = $1`,
		id, oc.status, attemptNo, next, oc.statusCode, oc.durationMs, clipUTF8(oc.errText, 1024), reqH, respH,
		oc.respBody, oc.truncated, delivered); err != nil {
		return err
	}
	if err := e.account(ctx, tx, webhookID, oc.status == "success", now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// send fa la richiesta e classifica l'esito: success (2xx), gone (410), failed
// (4xx diverso da 410, destinazione bloccata: non si ritenta) o retry (5xx,
// errore di rete, timeout).
func (e *Engine) send(ctx context.Context, deliveryID uuid.UUID, url, event string, payload []byte, secret string) outcome {
	hdr := map[string]string{
		"Content-Type":               "application/json",
		"User-Agent":                 UserAgent,
		"X-GitStack-Event":           event,
		"X-GitStack-Delivery":        deliveryID.String(),
		"X-GitStack-Payload-Version": fmt.Sprint(PayloadVersion),
	}
	if secret != "" {
		hdr["X-GitStack-Signature"] = Sign(secret, payload)
	}
	oc := outcome{reqHeader: hdr}
	rctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		oc.status, oc.errText = "failed", "richiesta non valida: "+err.Error()
		return oc
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	start := time.Now()
	resp, err := e.HTTP.Do(req)
	if err != nil {
		oc.durationMs = int(time.Since(start) / time.Millisecond)
		oc.errText = sanitizeErr(err)
		if egress.IsBlocked(err) {
			oc.status = "failed" // la destinazione non è ammessa: inutile ritentare
		} else {
			oc.status = "retry"
		}
		return oc
	}
	defer func() { _ = resp.Body.Close() }()
	// Un errore di lettura del corpo non cambia l'esito: conta il codice.
	body, trunc, _ := egress.ReadBody(resp.Body)
	oc.durationMs = int(time.Since(start) / time.Millisecond)
	code := resp.StatusCode
	oc.statusCode = &code
	oc.respBody = clipUTF8(string(body), MaxBody)
	oc.truncated = trunc
	oc.respHeader = flattenHeaders(resp.Header)
	switch {
	case code >= 200 && code < 300:
		oc.status = "success"
	case code == http.StatusGone:
		oc.status = "gone"
	case code >= 500:
		oc.status = "retry"
	default:
		oc.status = "failed"
	}
	return oc
}

func sanitizeErr(err error) string {
	s := err.Error()
	return clipUTF8(s, 1024)
}

func flattenHeaders(h http.Header) map[string]string {
	out := map[string]string{}
	n := 0
	for k, v := range h {
		if len(v) == 0 {
			continue
		}
		if n++; n > 50 {
			break
		}
		out[k] = clipUTF8(v[0], 512)
	}
	return out
}

// account tiene il conto dei fallimenti consecutivi del webhook (C7): un
// successo lo azzera, un fallimento lo avvia (se non c'è già) e, dopo 3
// giorni, disattiva il webhook e avvisa chi lo gestisce.
func (e *Engine) account(ctx context.Context, tx pgx.Tx, webhookID uuid.UUID, ok bool, now time.Time) error {
	if ok {
		_, err := tx.Exec(ctx, `UPDATE core.webhooks SET failing_since = NULL WHERE id = $1 AND failing_since IS NOT NULL`, webhookID)
		return err
	}
	var failingSince time.Time
	var active bool
	var scope, url string
	var repoID, orgID *uuid.UUID
	var createdBy uuid.UUID
	err := tx.QueryRow(ctx, `UPDATE core.webhooks SET failing_since = COALESCE(failing_since, $2) WHERE id = $1
		RETURNING failing_since, active, scope, url, repo_id, org_id, created_by`, webhookID, now).
		Scan(&failingSince, &active, &scope, &url, &repoID, &orgID, &createdBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !active || now.Sub(failingSince) < DisableAfter {
		return nil
	}
	recipients, err := e.managers(ctx, scope, repoID, orgID, createdBy)
	if err != nil {
		// Senza sapere chi avvisare non si disattiva in silenzio: il prossimo
		// fallimento ci riprova.
		e.Log.Warn("webhook: gestori non risolti, la disattivazione si rimanda", "webhook", webhookID, "err", err)
		return nil
	}
	if _, err := tx.Exec(ctx, `UPDATE core.webhooks SET active = false, disabled_at = $2, disabled_reason = 'consecutive_failures', updated_at = $2
		WHERE id = $1 AND active`, webhookID, now); err != nil {
		return err
	}
	data, _ := json.Marshal(map[string]any{"url": url, "scope": scope, "failingSince": failingSince.UTC().Format(time.RFC3339)})
	for _, u := range recipients {
		if _, err := tx.Exec(ctx, `INSERT INTO core.notifications (id, user_id, reason, repo_id, webhook_id, event_name, data, created_at)
			VALUES ($1, $2, 'webhook', $3, $4, 'webhook.disabled', $5, $6)`,
			uuid.New(), u, repoID, webhookID, data, now); err != nil {
			return fmt.Errorf("scrittura della notifica di disattivazione: %w", err)
		}
	}
	e.Log.Warn("webhook: disattivato dopo 3 giorni di fallimenti consecutivi", "webhook", webhookID, "notificati", len(recipients))
	return nil
}

func (e *Engine) managers(ctx context.Context, scope string, repoID, orgID *uuid.UUID, createdBy uuid.UUID) ([]uuid.UUID, error) {
	if scope == "org" {
		if e.Managers == nil || orgID == nil {
			return nil, nil
		}
		return e.Managers.OrgOwners(ctx, *orgID)
	}
	if e.Managers == nil || repoID == nil {
		return []uuid.UUID{createdBy}, nil
	}
	ok, err := e.Managers.HasRole(ctx, createdBy, *repoID, "admin")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return []uuid.UUID{createdBy}, nil
}
