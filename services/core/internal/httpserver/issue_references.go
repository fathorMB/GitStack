package httpserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"sort"
	"strings"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// extractReferences analizza un testo e ritorna i riferimenti a issue
// (#n per il repo corrente e owner/repo#n per repo esterni) che vi sono
// citati, ordinati per chiave e senza duplicati. Ignora il codice inline
// (backtick singoli) e i blocchi di codice (tre backtick), e il #0.
//
// La regex cattura due forme:
//   - owner/repo#n  (gruppi: owner, repo, numero)
//   - #n            (gruppo: numero)
//
// I blocchi ``` (fence) e l'inline `...` sono esclusi prima della ricerca.
func extractReferences(text string) []issueRef {
	cleaned := removeCodeBlocks(text)

	re := regexp.MustCompile(`(([a-zA-Z0-9_-]+)/([a-zA-Z0-9_-]+)#(\d+))|(#(\d+))`)
	matches := re.FindAllStringSubmatch(cleaned, -1)

	seen := map[string]bool{}
	var refs []issueRef

	for _, m := range matches {
		n := int64(0)
		var repoKey string

		if m[1] != "" {
			repoKey = strings.ToLower(m[2] + "/" + m[3])
			n = parseInt64(m[4])
		} else {
			repoKey = ""
			n = parseInt64(m[6])
		}

		if n <= 0 {
			continue
		}
		key := repoKey + ":" + itoa64(n)

		if seen[key] {
			continue
		}
		seen[key] = true
		refs = append(refs, issueRef{repoKey: repoKey, number: n})
	}

	sort.Slice(refs, func(i, j int) bool {
		if refs[i].repoKey != refs[j].repoKey {
			return refs[i].repoKey < refs[j].repoKey
		}
		return refs[i].number < refs[j].number
	})
	return refs
}

// removeCodeBlocks toglie i blocchi di codice fence ```...``` e l'inline `...`.
func removeCodeBlocks(text string) string {
	result := text

	for {
		idx := strings.Index(result, "```")
		if idx < 0 {
			break
		}
		end := strings.Index(result[idx+3:], "```")
		if end < 0 {
			result = result[:idx]
			continue
		}
		result = result[:idx] + result[idx+3+end+3:]
	}

	parts := strings.Split(result, "`")
	var cleaned []string
	for i, p := range parts {
		if i%2 == 1 {
			continue
		}
		cleaned = append(cleaned, p)
	}
	return strings.Join(cleaned, "")
}

// parseInt64 converte una stringa in int64, 0 su errore o valore non positivo.
func parseInt64(s string) int64 {
	n := int64(0)
	d := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int64(r-'0')
		d++
		if d > 18 {
			return 0
		}
	}
	if n == 0 {
		return 0
	}
	return n
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append(digits, byte('0'+n%10))
		n /= 10
	}
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	return string(digits)
}

// issueRef rappresenta un riferimento a una issue trovato nel testo.
type issueRef struct {
	repoKey string // "" = repo corrente, "owner/repo" = altro repo.
	number  int64
}

// processReferences elabora i riferimenti estratti dal testo: per ogni
// riferimento che non esiste ancora e per cui l'attore può leggere il
// repo di destinazione, inserisce in core.issue_references e scrive
// l'evento referenced_from.
//
// sourceID è l'ID della issue o del commento che contiene il riferimento.
// sourceKind è "issue" o "pull_request".
// sourceCommentID è nil se il riferimento è nel titolo/testo della issue.
func (s *apiServer) processReferences(
	ctx context.Context,
	tx pgx.Tx,
	actorID uuid.UUID,
	sourceRepoID uuid.UUID,
	sourceNumber int64,
	sourceID uuid.UUID,
	sourceCommentID *uuid.UUID,
	sourceKind string,
	sourceTitle string,
	text string,
) error {
	refs := extractReferences(text)
	if len(refs) == 0 {
		return nil
	}

	repoKeys := make(map[string]bool)
	for _, ref := range refs {
		if ref.repoKey != "" {
			repoKeys[ref.repoKey] = true
		}
	}

	repoMap := map[string]uuid.UUID{
		"": sourceRepoID,
	}

	for rk := range repoKeys {
		parts := strings.SplitN(rk, "/", 2)
		if len(parts) != 2 {
			continue
		}
		repo, err := s.resources.GetRepoByName(ctx, parts[0], parts[1])
		if err != nil {
			continue
		}
		repoMap[rk] = repo.ID
	}

	for _, ref := range refs {
		targetRepoID := repoMap[ref.repoKey]
		if targetRepoID == uuid.Nil {
			continue
		}

		commentArg := uuid.Nil
		if sourceCommentID != nil {
			commentArg = *sourceCommentID
		}

		tag, err := tx.Exec(ctx,
			`INSERT INTO core.issue_references (id, target_issue_id, source_kind, source_repo_id, source_number, source_comment_id, actor_id, created_at)
			SELECT gen_random_uuid(), i.id, $1, $2, $3, $4, $5, clock_timestamp()
			FROM core.issues i WHERE i.repo_id = $6 AND i.number = $7
			ON CONFLICT (target_issue_id, source_kind, source_repo_id, source_number,
				COALESCE(source_comment_id, '00000000-0000-0000-0000-000000000000'::uuid))
			DO NOTHING`,
			sourceKind, targetRepoID, ref.number, sourceCommentID,
			actorID, targetRepoID, ref.number)
		if err != nil {
			slog.Default().Warn("inserimento riferimento non riuscito", "err", err)
			continue
		}

		if tag.RowsAffected() == 0 {
			continue
		}

		canRead, err := s.repoIdentity.HasRole(ctx, actorID, targetRepoID, "read")
		if err != nil {
			slog.Default().Warn("verifica del permesso di lettura non riuscita", "err", err)
			continue
		}
		if !canRead {
			continue
		}

		if targetRepoID == sourceRepoID && ref.repoKey == "" && ref.number == sourceNumber {
			_, _ = tx.Exec(ctx,
				`DELETE FROM core.issue_references WHERE id = (
					SELECT id FROM core.issue_references
					WHERE target_issue_id = (SELECT id FROM core.issues WHERE repo_id = $1 AND number = $2)
					AND source_kind = $3 AND source_repo_id = $4 AND source_number = $5
					AND COALESCE(source_comment_id, '00000000-0000-0000-0000-000000000000'::uuid) = $6
					ORDER BY created_at DESC LIMIT 1)`,
				sourceRepoID, ref.number, sourceKind, sourceRepoID, ref.number, commentArg)
			continue
		}

		var targetIssueID uuid.UUID
		err = tx.QueryRow(ctx,
			`SELECT id FROM core.issues WHERE repo_id = $1 AND number = $2`,
			targetRepoID, ref.number).Scan(&targetIssueID)
		if err != nil {
			continue
		}

		refData := map[string]any{
			"source": map[string]any{
				"kind":       sourceKind,
				"repository": ref.repoKey,
				"number":     ref.number,
				"title":      sourceTitle,
			},
		}
		if sourceCommentID != nil {
			refData["source"].(map[string]any)["commentId"] = sourceCommentID.String()
		}

		if err := insertIssueEvent(ctx, tx, targetIssueID, "referenced_from", actorID, refData); err != nil {
			slog.Default().Warn("scrittura dell'evento referenced_from non riuscita", "err", err)
		}
	}

	return nil
}

// listIssueEventsFiltered legge tutti gli eventi della issue, filtra quelli
// referenced_from il cui repo sorgente il chiamante non può leggere, e
// applica la paginazione in Go. Rimuove sourceRepoId dai payload.
func (s *apiServer) listIssueEventsFiltered(
	ctx context.Context,
	pool *pgxpool.Pool,
	ia issueAccess,
	issueID uuid.UUID,
	page, perPage int,
) ([]openapi.IssueEvent, int, error) {
	rows, err := pool.Query(ctx, `SELECT id, type, actor_id, data, created_at FROM core.issue_events
		WHERE issue_id = $1 ORDER BY seq`, issueID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var rawEvents []rawEvent
	var actorIDs []uuid.UUID

	for rows.Next() {
		var ev openapi.IssueEvent
		var id uuid.UUID
		var typ string
		var actor *uuid.UUID
		var raw []byte
		if err := rows.Scan(&id, &typ, &actor, &raw, &ev.CreatedAt); err != nil {
			return nil, 0, err
		}
		ev.Id, ev.Type = openapi_types.UUID(id), openapi.IssueEventType(typ)
		var data map[string]any
		if err := json.Unmarshal(raw, &data); err != nil {
			return nil, 0, err
		}
		ev.Data = &data
		rawEvents = append(rawEvents, rawEvent{ev: ev, actor: actor})
		if actor != nil {
			actorIDs = append(actorIDs, *actor)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	dir, err := s.resolveUsers(ctx, actorIDs)
	if err != nil {
		return nil, 0, err
	}

	var filtered []openapi.IssueEvent
	for _, re := range rawEvents {
		ev := re.ev

		if ev.Type == "referenced_from" && ev.Data != nil {
			src, ok := (*ev.Data)["source"].(map[string]any)
			if ok {
				srcRepo, ok := src["repository"].(string)
				if ok && srcRepo != "" {
					parts := strings.SplitN(srcRepo, "/", 2)
					if len(parts) == 2 {
						repo, err := s.resources.GetRepoByName(ctx, parts[0], parts[1])
						if err == nil {
							canRead, _ := s.repoIdentity.HasRole(ctx, ia.userID, repo.ID, "read")
							if !canRead {
								continue
							}
						} else {
							continue
						}
					}
				}
			}
		}

		filtered = append(filtered, ev)
	}

	total := len(filtered)

	start := (page - 1) * perPage
	end := start + perPage
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}
	pageItems := filtered[start:end]

	result := make([]openapi.IssueEvent, 0, len(pageItems))
	for _, ev := range pageItems {
		var actorUUID *uuid.UUID
		for _, re := range rawEvents {
			if re.ev.Id == ev.Id {
				actorUUID = re.actor
				break
			}
		}
		if actorUUID != nil {
			u := dir[*actorUUID]
			ev.Actor = &u
		}

		if ev.Data != nil {
			delete(*ev.Data, "sourceRepoId")
		}

		result = append(result, ev)
	}

	return result, total, nil
}

type rawEvent struct {
	ev    openapi.IssueEvent
	actor *uuid.UUID
}
