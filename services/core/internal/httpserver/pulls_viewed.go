package httpserver

import (
	"context"
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
)

// Casella Viewed di Files changed (PR12b): personale (una riga per PR, utente
// e file) e legata alla versione del file. viewed_sha è l'head della PR al
// momento della spunta; se l'head è cambiato la casella resta solo se quel
// file non è cambiato nel frattempo (fra viewed_sha e l'head attuale). In
// ogni dubbio (viewed_sha non più antenato dell'head, confronto non
// disponibile, elenco dei file troncato) il file torna non visto.

// viewedPaths ritorna i file della PR che ia.userID ha marcato Viewed e che non
// sono cambiati da allora, in ordine di percorso.
func (s *apiServer) viewedPaths(ctx context.Context, g pullGit, p prRow, userID uuid.UUID, cur pullCompare) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT path, viewed_sha FROM core.pull_request_viewed_files
		WHERE pull_request_id = $1 AND user_id = $2 ORDER BY path`, p.ID, userID)
	if err != nil {
		return nil, err
	}
	type mark struct{ path, sha string }
	var marks []mark
	for rows.Next() {
		var m mark
		if err := rows.Scan(&m.path, &m.sha); err != nil {
			rows.Close()
			return nil, err
		}
		marks = append(marks, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	inPR := make(map[string]bool, len(cur.Files))
	for _, f := range cur.Files {
		inPR[f.Path] = true
	}
	changedSince := map[string]map[string]bool{} // viewed_sha -> file cambiati; nil = ignoto
	out := []string{}
	for _, m := range marks {
		if !inPR[m.path] {
			continue
		}
		if m.sha == cur.HeadSha {
			out = append(out, m.path)
			continue
		}
		ch, known := changedSince[m.sha]
		if !known {
			ch = nil
			if c, err := g.compare(ctx, m.sha, cur.HeadSha, false); err == nil && c.MergeBase == m.sha && !c.Truncated && !c.ListOnly {
				ch = make(map[string]bool, len(c.Files))
				for _, f := range c.Files {
					ch[f.Path] = true
					if f.OldPath != nil {
						ch[*f.OldPath] = true
					}
				}
			}
			changedSince[m.sha] = ch
		}
		if ch != nil && !ch[m.path] {
			out = append(out, m.path)
		}
	}
	return out, nil
}

// SetPullRequestFileViewed implementa PUT .../pulls/{number}/viewed.
func (s *apiServer) SetPullRequestFileViewed(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.PullNumberParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok || ia.rejectArchivedPull(w) {
		return
	}
	var in openapi.SetFileViewedInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if in.Path == "" || len(in.Path) > 4096 {
		writeFieldError(w, "path", "path: da 1 a 4096 caratteri.")
		return
	}
	p, c, ok := s.pullDiff(w, r, ia, number, false)
	if !ok {
		return
	}
	found := false
	for _, f := range c.Files {
		found = found || f.Path == in.Path
	}
	if !found {
		writeFieldError(w, "path", "path: non è un file di questa Pull Request.")
		return
	}
	ctx := r.Context()
	var err error
	if in.Viewed {
		_, err = s.pool.Exec(ctx, `INSERT INTO core.pull_request_viewed_files (pull_request_id, user_id, path, viewed_sha)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (pull_request_id, user_id, path) DO UPDATE SET viewed_sha = EXCLUDED.viewed_sha, viewed_at = now()`,
			p.ID, ia.userID, in.Path, c.HeadSha)
	} else {
		_, err = s.pool.Exec(ctx, `DELETE FROM core.pull_request_viewed_files WHERE pull_request_id = $1 AND user_id = $2 AND path = $3`,
			p.ID, ia.userID, in.Path)
	}
	if err != nil {
		writeIssueFailure(w, "salvataggio del file Viewed non riuscito", err)
		return
	}
	writeJSON(w, http.StatusOK, in)
}

// rejectArchivedPull risponde 409 `archived` se il repo è archiviato (R10).
func (ia issueAccess) rejectArchivedPull(w http.ResponseWriter) bool {
	if ia.repo.ArchivedAt != nil {
		writePullArchived(w)
		return true
	}
	return false
}
