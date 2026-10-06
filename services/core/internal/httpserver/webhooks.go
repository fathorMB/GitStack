package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/webhooks"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Webhook di repo e di organizzazione, log delle consegne e Redeliver
// (M-06/G, GIT-135; regole C6 e C7). Un webhook di repo lo gestisce chi ha
// `admin` sul repo, uno di organizzazione gli owner (e l'amministratore
// dell'installazione). Un repo che non si legge è 404, chi lo legge senza
// admin è 403. La consegna vera è in internal/webhooks.

// WebhookConfig imposta cifratura dei segreti e validazione degli indirizzi.
type WebhookConfig struct {
	// Keys cifra i segreti; nil: un segreto risponde 503 (i webhook senza
	// segreto funzionano lo stesso).
	Keys *webhooks.Keyring
	// CheckURL rifiuta gli indirizzi non ammessi (C8); nil: solo i controlli
	// sul testo (schema, localhost).
	CheckURL webhooks.URLChecker
}

// hookScope è il bersaglio di una richiesta: un repo o un'organizzazione, già
// verificati i permessi del chiamante.
type hookScope struct {
	repo   *store.Repo
	org    *identityclient.Owner
	userID uuid.UUID
}

func (sc hookScope) scope() string {
	if sc.repo != nil {
		return "repo"
	}
	return "org"
}

func (sc hookScope) targetID() uuid.UUID {
	if sc.repo != nil {
		return sc.repo.ID
	}
	return sc.org.ID
}

// cond è la clausola che lega un webhook al bersaglio (argomento $1).
func (sc hookScope) cond() string {
	if sc.repo != nil {
		return `w.scope = 'repo' AND w.repo_id = $1`
	}
	return `w.scope = 'org' AND w.org_id = $1`
}

func (s *apiServer) hooksReady(w http.ResponseWriter) bool {
	if s.repoIdentity == nil {
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non configurata: impossibile applicare i permessi sui webhook.")
		return false
	}
	return true
}

// hookRepoScope risolve il repo e verifica admin. mutate: rifiuta anche un
// repo archiviato (R10).
func (s *apiServer) hookRepoScope(w http.ResponseWriter, r *http.Request, owner, name string, mutate bool) (hookScope, bool) {
	_, userID, ok := callerIdentity(w, r)
	if !ok || !s.hooksReady(w) {
		return hookScope{}, false
	}
	repo, ok := s.lookupReadable(w, r, userID, owner, name)
	if !ok {
		return hookScope{}, false
	}
	admin, err := s.repoIdentity.HasRole(r.Context(), userID, repo.ID, "admin")
	if err != nil {
		slog.Default().Warn("verifica del permesso admin non riuscita", "err", err)
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile: impossibile verificare i permessi.")
		return hookScope{}, false
	}
	if !admin {
		writeError(w, http.StatusForbidden, "forbidden", "Serve il permesso admin sul repo.")
		return hookScope{}, false
	}
	if mutate && repo.ArchivedAt != nil {
		writeError(w, http.StatusConflict, "archived", "Il repo è archiviato: non accetta modifiche.")
		return hookScope{}, false
	}
	return hookScope{repo: &repo, userID: userID}, true
}

// hookOrgScope risolve l'organizzazione e verifica il ruolo owner.
func (s *apiServer) hookOrgScope(w http.ResponseWriter, r *http.Request, org string) (hookScope, bool) {
	_, userID, ok := callerIdentity(w, r)
	if !ok || !s.hooksReady(w) {
		return hookScope{}, false
	}
	lister, ok := s.repoIdentity.(identityclient.OrgOwnerLister)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non configurata: impossibile verificare gli owner.")
		return hookScope{}, false
	}
	unavailable := func(err error) {
		slog.Default().Warn("verifica del ruolo owner non riuscita", "err", err)
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile: impossibile verificare i permessi.")
	}
	owner, err := s.repoIdentity.ResolveOwner(r.Context(), org)
	if errors.Is(err, identityclient.ErrNotFound) || (err == nil && owner.Type != "organization") {
		writeError(w, http.StatusNotFound, "not_found", "Organizzazione non trovata.")
		return hookScope{}, false
	}
	if err != nil {
		unavailable(err)
		return hookScope{}, false
	}
	owners, err := lister.OrgOwners(r.Context(), owner.ID)
	if errors.Is(err, identityclient.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Organizzazione non trovata.")
		return hookScope{}, false
	}
	if err != nil {
		unavailable(err)
		return hookScope{}, false
	}
	if !slices.Contains(owners, userID) {
		// L'amministratore dell'installazione gestisce tutto.
		all, _, err := s.readable.ReadableResources(r.Context(), userID)
		if err != nil {
			unavailable(err)
			return hookScope{}, false
		}
		if !all {
			writeError(w, http.StatusForbidden, "forbidden", "Serve il ruolo owner dell'organizzazione.")
			return hookScope{}, false
		}
	}
	return hookScope{org: &owner, userID: userID}, true
}

// ---------------------------------------------------------------------------
// Righe e conversioni

type hookRow struct {
	id             uuid.UUID
	url            string
	events         []string
	active         bool
	hasSecret      bool
	failingSince   *time.Time
	disabledAt     *time.Time
	disabledReason *string
	createdBy      uuid.UUID
	createdAt      time.Time
	updatedAt      time.Time
}

const hookCols = `w.id, w.url, w.events, w.active, w.secret_ciphertext IS NOT NULL, w.failing_since, w.disabled_at,
	w.disabled_reason, w.created_by, w.created_at, w.updated_at`

func scanHook(row pgx.Row) (hookRow, error) {
	var h hookRow
	err := row.Scan(&h.id, &h.url, &h.events, &h.active, &h.hasSecret, &h.failingSince, &h.disabledAt,
		&h.disabledReason, &h.createdBy, &h.createdAt, &h.updatedAt)
	return h, err
}

type deliveryRow struct {
	id            uuid.UUID
	hookID        uuid.UUID
	event, action string
	status        string
	attempt       int
	nextAttemptAt *time.Time
	statusCode    *int
	durationMs    *int
	errText       string
	redeliveryOf  *uuid.UUID
	createdAt     time.Time
	deliveredAt   *time.Time
}

const deliveryCols = `d.id, d.webhook_id, d.event, d.action, d.status, d.attempt, d.next_attempt_at, d.status_code, d.duration_ms,
	d.error, d.redelivery_of, d.created_at, d.delivered_at`

func scanDelivery(row pgx.Row) (deliveryRow, error) {
	var d deliveryRow
	err := row.Scan(&d.id, &d.hookID, &d.event, &d.action, &d.status, &d.attempt, &d.nextAttemptAt, &d.statusCode, &d.durationMs,
		&d.errText, &d.redeliveryOf, &d.createdAt, &d.deliveredAt)
	return d, err
}

func (d deliveryRow) summary() openapi.WebhookDeliverySummary {
	out := openapi.WebhookDeliverySummary{
		Id: openapi_types.UUID(d.id), Event: openapi.WebhookEvent(d.event), Status: openapi.WebhookDeliveryStatus(d.status),
		Attempt: d.attempt, NextAttemptAt: d.nextAttemptAt, StatusCode: d.statusCode, DurationMs: d.durationMs,
		CreatedAt: d.createdAt, DeliveredAt: d.deliveredAt,
	}
	if d.action != "" {
		a := d.action
		out.Action = &a
	}
	if d.errText != "" {
		e := d.errText
		out.Error = &e
	}
	if d.redeliveryOf != nil {
		r := openapi_types.UUID(*d.redeliveryOf)
		out.RedeliveryOf = &r
	}
	return out
}

func (s *apiServer) hookViews(ctx context.Context, sc hookScope, rows []hookRow) ([]openapi.Webhook, error) {
	ids := make([]uuid.UUID, 0, len(rows))
	users := make([]uuid.UUID, 0, len(rows))
	for _, h := range rows {
		ids = append(ids, h.id)
		users = append(users, h.createdBy)
	}
	last := map[uuid.UUID]openapi.WebhookDeliverySummary{}
	if len(ids) > 0 {
		dr, err := s.pool.Query(ctx, `SELECT DISTINCT ON (d.webhook_id) `+deliveryCols+` FROM core.webhook_deliveries d
			WHERE d.webhook_id = ANY($1) ORDER BY d.webhook_id, d.created_at DESC, d.id`, ids)
		if err != nil {
			return nil, err
		}
		for dr.Next() {
			d, err := scanDelivery(dr)
			if err != nil {
				dr.Close()
				return nil, err
			}
			last[d.hookID] = d.summary()
		}
		dr.Close()
		if err := dr.Err(); err != nil {
			return nil, err
		}
	}
	dir, err := s.resolveUsers(ctx, users)
	if err != nil {
		return nil, err
	}
	out := make([]openapi.Webhook, 0, len(rows))
	for _, h := range rows {
		w := openapi.Webhook{
			Id: openapi_types.UUID(h.id), Scope: openapi.WebhookScope(sc.scope()), Url: h.url, Active: h.active, HasSecret: h.hasSecret,
			DisabledAt: h.disabledAt, FailingSince: h.failingSince, CreatedAt: h.createdAt, UpdatedAt: h.updatedAt,
		}
		for _, ev := range h.events {
			w.Events = append(w.Events, openapi.WebhookEvent(ev))
		}
		if h.disabledReason != nil {
			dr := openapi.WebhookDisabledReason(*h.disabledReason)
			w.DisabledReason = &dr
		}
		if sc.repo != nil {
			w.Repository = &openapi.NotificationRepo{Id: openapi_types.UUID(sc.repo.ID), FullName: sc.repo.OwnerName + "/" + sc.repo.Name}
		} else {
			name := sc.org.Name
			w.Organization = &name
		}
		if u, ok := dir[h.createdBy]; ok {
			w.CreatedBy = &u
		}
		if d, ok := last[h.id]; ok {
			w.LastDelivery = &d
		}
		out = append(out, w)
	}
	return out, nil
}

func (s *apiServer) writeHookFailure(w http.ResponseWriter, what string, err error) {
	writeIssueFailure(w, what, err)
}

// ---------------------------------------------------------------------------
// Operazioni condivise

func (s *apiServer) listHooks(w http.ResponseWriter, r *http.Request, sc hookScope, page, perPage *int) {
	pg, pp, ok := pageParams(w, page, perPage)
	if !ok {
		return
	}
	ctx := r.Context()
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM core.webhooks w WHERE `+sc.cond(), sc.targetID()).Scan(&total); err != nil {
		s.writeHookFailure(w, "lettura dei webhook non riuscita", err)
		return
	}
	rows, err := s.pool.Query(ctx, `SELECT `+hookCols+` FROM core.webhooks w WHERE `+sc.cond()+`
		ORDER BY w.created_at, w.id LIMIT $2 OFFSET $3`, sc.targetID(), pp, (pg-1)*pp)
	if err != nil {
		s.writeHookFailure(w, "lettura dei webhook non riuscita", err)
		return
	}
	var list []hookRow
	for rows.Next() {
		h, err := scanHook(rows)
		if err != nil {
			rows.Close()
			s.writeHookFailure(w, "lettura dei webhook non riuscita", err)
			return
		}
		list = append(list, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		s.writeHookFailure(w, "lettura dei webhook non riuscita", err)
		return
	}
	views, err := s.hookViews(ctx, sc, list)
	if err != nil {
		s.writeHookFailure(w, "composizione dei webhook non riuscita", err)
		return
	}
	writeJSON(w, http.StatusOK, openapi.WebhookList{Items: views, Page: pg, PerPage: pp, Total: total})
}

func writeHookURLError(w http.ResponseWriter, msg string) {
	var body openapi.Error
	body.Error.Code = "url_not_allowed"
	body.Error.Message = msg
	d := map[string]interface{}{"fields": map[string]interface{}{"url": msg}}
	body.Error.Details = &d
	writeJSON(w, http.StatusUnprocessableEntity, body)
}

// validateHookURL: 1-2048 caratteri e un indirizzo ammesso (C8).
func (s *apiServer) validateHookURL(w http.ResponseWriter, raw string) bool {
	if raw == "" || len(raw) > 2048 {
		writeFieldError(w, "url", "url: da 1 a 2048 caratteri.")
		return false
	}
	check := s.hooks.CheckURL
	if check == nil {
		check = webhooks.CheckURL(nil)
	}
	if err := check(raw); err != nil {
		writeHookURLError(w, err.Error())
		return false
	}
	return true
}

// validateHookEvents: almeno uno, senza ripetizioni, fra quelli selezionabili.
func validateHookEvents(w http.ResponseWriter, events []string) bool {
	if len(events) < 1 {
		writeFieldError(w, "events", "events: serve almeno un evento fra push, issues, issue_comment e repository.")
		return false
	}
	seen := map[string]bool{}
	for _, e := range events {
		if !slices.Contains(webhooks.Events, e) || seen[e] {
			writeFieldError(w, "events", "events: valori ammessi push, issues, issue_comment e repository, senza ripetizioni.")
			return false
		}
		seen[e] = true
	}
	return true
}

const maxHookSecret = 256

// sealSecret cifra il segreto; ok=false dopo aver risposto.
func (s *apiServer) sealSecret(w http.ResponseWriter, id uuid.UUID, secret string) (ct, nonce []byte, keyID string, ok bool) {
	if len(secret) > maxHookSecret {
		writeFieldError(w, "secret", "secret: al massimo 256 caratteri.")
		return nil, nil, "", false
	}
	ct, nonce, keyID, err := s.hooks.Keys.Seal(id, secret)
	if errors.Is(err, webhooks.ErrNoKey) {
		writeError(w, http.StatusServiceUnavailable, "webhook_secrets_unavailable", "I segreti dei webhook non sono configurati su questo server (GITSTACK_WEBHOOK_SECRET_KEY).")
		return nil, nil, "", false
	}
	if err != nil {
		s.writeHookFailure(w, "cifratura del segreto non riuscita", err)
		return nil, nil, "", false
	}
	return ct, nonce, keyID, true
}

func (s *apiServer) createHook(w http.ResponseWriter, r *http.Request, sc hookScope) {
	var in openapi.CreateWebhookInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	events := make([]string, 0, len(in.Events))
	for _, e := range in.Events {
		events = append(events, string(e))
	}
	if !validateHookEvents(w, events) || !s.validateHookURL(w, in.Url) {
		return
	}
	id := uuid.New()
	var ct, nonce []byte
	var keyID *string
	if in.Secret != nil && *in.Secret != "" {
		c, n, k, ok := s.sealSecret(w, id, *in.Secret)
		if !ok {
			return
		}
		ct, nonce, keyID = c, n, &k
	}
	active := true
	if in.Active != nil {
		active = *in.Active
	}
	var repoID, orgID *uuid.UUID
	if sc.repo != nil {
		repoID = &sc.repo.ID
	} else {
		orgID = &sc.org.ID
	}
	row := s.pool.QueryRow(r.Context(), `INSERT INTO core.webhooks AS w
			(id, scope, repo_id, org_id, url, events, active, secret_ciphertext, secret_nonce, secret_key_id, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING `+hookCols, id, sc.scope(), repoID, orgID, in.Url, events, active, ct, nonce, keyID, sc.userID)
	h, err := scanHook(row)
	if err != nil {
		s.writeHookFailure(w, "creazione del webhook non riuscita", err)
		return
	}
	views, err := s.hookViews(r.Context(), sc, []hookRow{h})
	if err != nil {
		s.writeHookFailure(w, "composizione del webhook non riuscita", err)
		return
	}
	writeJSON(w, http.StatusCreated, views[0])
}

func writeHookNotFound(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "not_found", "Webhook non trovato.")
}

func (s *apiServer) loadHook(w http.ResponseWriter, ctx context.Context, q querier, sc hookScope, id uuid.UUID, forUpdate bool) (hookRow, bool) {
	sql := `SELECT ` + hookCols + ` FROM core.webhooks w WHERE ` + sc.cond() + ` AND w.id = $2`
	if forUpdate {
		sql += ` FOR UPDATE OF w`
	}
	h, err := scanHook(q.QueryRow(ctx, sql, sc.targetID(), id))
	if errors.Is(err, pgx.ErrNoRows) {
		writeHookNotFound(w)
		return hookRow{}, false
	}
	if err != nil {
		s.writeHookFailure(w, "lettura del webhook non riuscita", err)
		return hookRow{}, false
	}
	return h, true
}

func (s *apiServer) respondHook(w http.ResponseWriter, ctx context.Context, sc hookScope, id uuid.UUID, status int) {
	h, ok := s.loadHook(w, ctx, s.pool, sc, id, false)
	if !ok {
		return
	}
	views, err := s.hookViews(ctx, sc, []hookRow{h})
	if err != nil {
		s.writeHookFailure(w, "composizione del webhook non riuscita", err)
		return
	}
	writeJSON(w, status, views[0])
}

func (s *apiServer) getHook(w http.ResponseWriter, r *http.Request, sc hookScope, id uuid.UUID) {
	s.respondHook(w, r.Context(), sc, id, http.StatusOK)
}

func (s *apiServer) updateHook(w http.ResponseWriter, r *http.Request, sc hookScope, id uuid.UUID) {
	var in struct {
		URL    *string   `json:"url"`
		Events *[]string `json:"events"`
		Secret *string   `json:"secret"`
		Active *bool     `json:"active"`
	}
	if !decodeIssueJSON(w, r, &in, true) {
		return
	}
	if in.URL != nil && !s.validateHookURL(w, *in.URL) {
		return
	}
	if in.Events != nil && !validateHookEvents(w, *in.Events) {
		return
	}
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.writeHookFailure(w, "modifica del webhook non riuscita", err)
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	h, ok := s.loadHook(w, ctx, tx, sc, id, true)
	if !ok {
		return
	}
	if in.Active != nil && *in.Active && !h.active && h.disabledReason != nil {
		writeError(w, http.StatusConflict, "webhook_disabled", "Il webhook è stato disattivato dai fallimenti: si riattiva con reactivate.")
		return
	}
	if in.URL != nil {
		h.url = *in.URL
	}
	if in.Events != nil {
		h.events = *in.Events
	}
	if in.Active != nil {
		h.active = *in.Active
	}
	if _, err := tx.Exec(ctx, `UPDATE core.webhooks SET url = $2, events = $3, active = $4, updated_at = now() WHERE id = $1`,
		id, h.url, h.events, h.active); err != nil {
		s.writeHookFailure(w, "modifica del webhook non riuscita", err)
		return
	}
	if in.Secret != nil {
		if *in.Secret == "" {
			_, err = tx.Exec(ctx, `UPDATE core.webhooks SET secret_ciphertext = NULL, secret_nonce = NULL, secret_key_id = NULL WHERE id = $1`, id)
		} else {
			ct, nonce, keyID, ok := s.sealSecret(w, id, *in.Secret)
			if !ok {
				return
			}
			_, err = tx.Exec(ctx, `UPDATE core.webhooks SET secret_ciphertext = $2, secret_nonce = $3, secret_key_id = $4 WHERE id = $1`, id, ct, nonce, keyID)
		}
		if err != nil {
			s.writeHookFailure(w, "modifica del segreto non riuscita", err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		s.writeHookFailure(w, "modifica del webhook non riuscita", err)
		return
	}
	s.respondHook(w, ctx, sc, id, http.StatusOK)
}

func (s *apiServer) deleteHook(w http.ResponseWriter, r *http.Request, sc hookScope, id uuid.UUID) {
	tag, err := s.pool.Exec(r.Context(), `DELETE FROM core.webhooks w WHERE `+sc.cond()+` AND w.id = $2`, sc.targetID(), id)
	if err != nil {
		s.writeHookFailure(w, "eliminazione del webhook non riuscita", err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeHookNotFound(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// reactivateHook riattiva un webhook (a mano o dopo i fallimenti): azzera il
// conto dei fallimenti. Idempotente.
func (s *apiServer) reactivateHook(w http.ResponseWriter, r *http.Request, sc hookScope, id uuid.UUID) {
	tag, err := s.pool.Exec(r.Context(), `UPDATE core.webhooks w SET active = true, failing_since = NULL, disabled_at = NULL,
			disabled_reason = NULL, updated_at = CASE WHEN w.active AND w.failing_since IS NULL THEN w.updated_at ELSE now() END
		WHERE `+sc.cond()+` AND w.id = $2`, sc.targetID(), id)
	if err != nil {
		s.writeHookFailure(w, "riattivazione del webhook non riuscita", err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeHookNotFound(w)
		return
	}
	s.respondHook(w, r.Context(), sc, id, http.StatusOK)
}

func (s *apiServer) listDeliveries(w http.ResponseWriter, r *http.Request, sc hookScope, id uuid.UUID, status *openapi.WebhookDeliveryStatus, page, perPage *int) {
	pg, pp, ok := pageParams(w, page, perPage)
	if !ok {
		return
	}
	ctx := r.Context()
	if _, ok := s.loadHook(w, ctx, s.pool, sc, id, false); !ok {
		return
	}
	filter := ""
	args := []any{id}
	if status != nil {
		if !slices.Contains([]string{"pending", "success", "failed", "gone"}, string(*status)) {
			writeError(w, http.StatusBadRequest, "invalid_status", "status: pending, success, failed o gone.")
			return
		}
		filter = ` AND d.status = $2`
		args = append(args, string(*status))
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM core.webhook_deliveries d WHERE d.webhook_id = $1`+filter, args...).Scan(&total); err != nil {
		s.writeHookFailure(w, "lettura del log non riuscita", err)
		return
	}
	args = append(args, pp, (pg-1)*pp)
	rows, err := s.pool.Query(ctx, `SELECT `+deliveryCols+` FROM core.webhook_deliveries d WHERE d.webhook_id = $1`+filter+
		` ORDER BY d.created_at DESC, d.id LIMIT $`+itoa(len(args)-1)+` OFFSET $`+itoa(len(args)), args...)
	if err != nil {
		s.writeHookFailure(w, "lettura del log non riuscita", err)
		return
	}
	defer rows.Close()
	items := []openapi.WebhookDeliverySummary{}
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			s.writeHookFailure(w, "lettura del log non riuscita", err)
			return
		}
		items = append(items, d.summary())
	}
	if err := rows.Err(); err != nil {
		s.writeHookFailure(w, "lettura del log non riuscita", err)
		return
	}
	writeJSON(w, http.StatusOK, openapi.WebhookDeliveryList{Items: items, Page: pg, PerPage: pp, Total: total})
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func (s *apiServer) getDelivery(w http.ResponseWriter, r *http.Request, sc hookScope, hookID, deliveryID uuid.UUID) {
	ctx := r.Context()
	if _, ok := s.loadHook(w, ctx, s.pool, sc, hookID, false); !ok {
		return
	}
	var reqHeaders, respHeaders, payload []byte
	var body string
	var truncated bool
	row := s.pool.QueryRow(ctx, `SELECT `+deliveryCols+`, d.request_headers, d.payload, d.response_headers, d.response_body, d.response_truncated
		FROM core.webhook_deliveries d WHERE d.id = $1 AND d.webhook_id = $2`, deliveryID, hookID)
	var d deliveryRow
	err := row.Scan(&d.id, &d.hookID, &d.event, &d.action, &d.status, &d.attempt, &d.nextAttemptAt, &d.statusCode, &d.durationMs,
		&d.errText, &d.redeliveryOf, &d.createdAt, &d.deliveredAt, &reqHeaders, &payload, &respHeaders, &body, &truncated)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "Consegna non trovata.")
		return
	}
	if err != nil {
		s.writeHookFailure(w, "lettura della consegna non riuscita", err)
		return
	}
	detail := openapi.WebhookDeliveryDetail{
		Attempt: d.attempt, CreatedAt: d.createdAt, DeliveredAt: d.deliveredAt, DurationMs: d.durationMs,
		Event: openapi.WebhookEvent(d.event), Id: openapi_types.UUID(d.id), NextAttemptAt: d.nextAttemptAt,
		Status: openapi.WebhookDeliveryStatus(d.status), StatusCode: d.statusCode,
	}
	if d.action != "" {
		a := d.action
		detail.Action = &a
	}
	if d.errText != "" {
		e := d.errText
		detail.Error = &e
	}
	if d.redeliveryOf != nil {
		rd := openapi_types.UUID(*d.redeliveryOf)
		detail.RedeliveryOf = &rd
	}
	detail.Request.Headers = map[string]string{}
	_ = json.Unmarshal(reqHeaders, &detail.Request.Headers)
	detail.Request.Payload = map[string]interface{}{}
	_ = json.Unmarshal(payload, &detail.Request.Payload)
	if d.statusCode != nil {
		resp := openapi.WebhookDeliveryResponse{Body: body, Truncated: truncated, Headers: map[string]string{}}
		_ = json.Unmarshal(respHeaders, &resp.Headers)
		detail.Response = &resp
	}
	writeJSON(w, http.StatusOK, detail)
}

// redeliver crea una consegna nuova con lo stesso payload (C7); la invia il
// motore alla prima occasione. Non guarda se il webhook è attivo.
func (s *apiServer) redeliver(w http.ResponseWriter, r *http.Request, sc hookScope, hookID, deliveryID uuid.UUID) {
	ctx := r.Context()
	if _, ok := s.loadHook(w, ctx, s.pool, sc, hookID, false); !ok {
		return
	}
	row := s.pool.QueryRow(ctx, `INSERT INTO core.webhook_deliveries AS d
			(id, webhook_id, event, action, status, next_attempt_at, payload, redelivery_of, created_at)
		SELECT $3, o.webhook_id, o.event, o.action, 'pending', $4, o.payload, o.id, $4
		FROM core.webhook_deliveries o WHERE o.id = $1 AND o.webhook_id = $2
		RETURNING `+deliveryCols, deliveryID, hookID, uuid.New(), s.now())
	d, err := scanDelivery(row)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "Consegna non trovata.")
		return
	}
	if err != nil {
		s.writeHookFailure(w, "Redeliver non riuscito", err)
		return
	}
	writeJSON(w, http.StatusAccepted, d.summary())
}

// ---------------------------------------------------------------------------
// Operazioni del contratto: repo

func (s *apiServer) ListRepoWebhooks(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, params openapi.ListRepoWebhooksParams) {
	if sc, ok := s.hookRepoScope(w, r, owner, name, false); ok {
		s.listHooks(w, r, sc, params.Page, params.PerPage)
	}
}

func (s *apiServer) CreateRepoWebhook(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam) {
	if sc, ok := s.hookRepoScope(w, r, owner, name, true); ok {
		s.createHook(w, r, sc)
	}
}

func (s *apiServer) GetRepoWebhook(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, id openapi.WebhookIdParam) {
	if sc, ok := s.hookRepoScope(w, r, owner, name, false); ok {
		s.getHook(w, r, sc, id)
	}
}

func (s *apiServer) UpdateRepoWebhook(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, id openapi.WebhookIdParam) {
	if sc, ok := s.hookRepoScope(w, r, owner, name, true); ok {
		s.updateHook(w, r, sc, id)
	}
}

func (s *apiServer) DeleteRepoWebhook(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, id openapi.WebhookIdParam) {
	if sc, ok := s.hookRepoScope(w, r, owner, name, true); ok {
		s.deleteHook(w, r, sc, id)
	}
}

func (s *apiServer) ReactivateRepoWebhook(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, id openapi.WebhookIdParam) {
	if sc, ok := s.hookRepoScope(w, r, owner, name, true); ok {
		s.reactivateHook(w, r, sc, id)
	}
}

func (s *apiServer) ListRepoWebhookDeliveries(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, id openapi.WebhookIdParam, params openapi.ListRepoWebhookDeliveriesParams) {
	if sc, ok := s.hookRepoScope(w, r, owner, name, false); ok {
		s.listDeliveries(w, r, sc, id, params.Status, params.Page, params.PerPage)
	}
}

func (s *apiServer) GetRepoWebhookDelivery(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, id openapi.WebhookIdParam, deliveryID openapi.WebhookDeliveryIdParam) {
	if sc, ok := s.hookRepoScope(w, r, owner, name, false); ok {
		s.getDelivery(w, r, sc, id, deliveryID)
	}
}

func (s *apiServer) RedeliverRepoWebhookDelivery(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, id openapi.WebhookIdParam, deliveryID openapi.WebhookDeliveryIdParam) {
	if sc, ok := s.hookRepoScope(w, r, owner, name, false); ok {
		s.redeliver(w, r, sc, id, deliveryID)
	}
}

// ---------------------------------------------------------------------------
// Operazioni del contratto: organizzazione

func (s *apiServer) ListOrgWebhooks(w http.ResponseWriter, r *http.Request, org openapi.OrgParam, params openapi.ListOrgWebhooksParams) {
	if sc, ok := s.hookOrgScope(w, r, org); ok {
		s.listHooks(w, r, sc, params.Page, params.PerPage)
	}
}

func (s *apiServer) CreateOrgWebhook(w http.ResponseWriter, r *http.Request, org openapi.OrgParam) {
	if sc, ok := s.hookOrgScope(w, r, org); ok {
		s.createHook(w, r, sc)
	}
}

func (s *apiServer) GetOrgWebhook(w http.ResponseWriter, r *http.Request, org openapi.OrgParam, id openapi.WebhookIdParam) {
	if sc, ok := s.hookOrgScope(w, r, org); ok {
		s.getHook(w, r, sc, id)
	}
}

func (s *apiServer) UpdateOrgWebhook(w http.ResponseWriter, r *http.Request, org openapi.OrgParam, id openapi.WebhookIdParam) {
	if sc, ok := s.hookOrgScope(w, r, org); ok {
		s.updateHook(w, r, sc, id)
	}
}

func (s *apiServer) DeleteOrgWebhook(w http.ResponseWriter, r *http.Request, org openapi.OrgParam, id openapi.WebhookIdParam) {
	if sc, ok := s.hookOrgScope(w, r, org); ok {
		s.deleteHook(w, r, sc, id)
	}
}

func (s *apiServer) ReactivateOrgWebhook(w http.ResponseWriter, r *http.Request, org openapi.OrgParam, id openapi.WebhookIdParam) {
	if sc, ok := s.hookOrgScope(w, r, org); ok {
		s.reactivateHook(w, r, sc, id)
	}
}

func (s *apiServer) ListOrgWebhookDeliveries(w http.ResponseWriter, r *http.Request, org openapi.OrgParam, id openapi.WebhookIdParam, params openapi.ListOrgWebhookDeliveriesParams) {
	if sc, ok := s.hookOrgScope(w, r, org); ok {
		s.listDeliveries(w, r, sc, id, params.Status, params.Page, params.PerPage)
	}
}

func (s *apiServer) GetOrgWebhookDelivery(w http.ResponseWriter, r *http.Request, org openapi.OrgParam, id openapi.WebhookIdParam, deliveryID openapi.WebhookDeliveryIdParam) {
	if sc, ok := s.hookOrgScope(w, r, org); ok {
		s.getDelivery(w, r, sc, id, deliveryID)
	}
}

func (s *apiServer) RedeliverOrgWebhookDelivery(w http.ResponseWriter, r *http.Request, org openapi.OrgParam, id openapi.WebhookIdParam, deliveryID openapi.WebhookDeliveryIdParam) {
	if sc, ok := s.hookOrgScope(w, r, org); ok {
		s.redeliver(w, r, sc, id, deliveryID)
	}
}
