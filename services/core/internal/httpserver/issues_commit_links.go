package httpserver

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// commitRepoFilter toglie dalla cronologia i collegamenti e le chiusure da
// commit il cui repo ($2, text[]) il chiamante non legge (C1, C2).
const commitRepoFilter = ` AND NOT (type IN ('commit_linked', 'closed_by_commit') AND data->'commit'->>'repositoryId' = ANY($2::text[]))`

// unreadableCommitRepos elenca i repo dei commit collegati alla issue (diversi
// da quello della issue) che il chiamante non legge. Chi vede la issue vede
// già il suo repo; il commit può stare in un altro repo e il chiamante deve
// vedere entrambi.
func (s *apiServer) unreadableCommitRepos(ctx context.Context, ia issueAccess, issueID uuid.UUID) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT data->'commit'->>'repositoryId' FROM core.issue_events
		WHERE issue_id = $1 AND type IN ('commit_linked', 'closed_by_commit')
		AND data->'commit'->>'repositoryId' IS NOT NULL AND data->'commit'->>'repositoryId' <> $2`, issueID, ia.repo.ID.String())
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	hidden := []string{}
	for _, id := range ids {
		rid, err := uuid.Parse(id)
		if err != nil {
			hidden = append(hidden, id)
			continue
		}
		ok, err := s.repoIdentity.HasRole(ctx, ia.userID, rid, "read")
		if err != nil {
			return nil, fmt.Errorf("permesso read sul repo %s: %w", id, err)
		}
		if !ok {
			hidden = append(hidden, id)
		}
	}
	return hidden, nil
}
