package httpserver

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// Issues (M-05, GIT-101): il contratto e lo schema (migrazione 0004) sono
// fissati, le operazioni rispondono 501 finché gli item a valle non le
// implementano. Regole I1-I11: README di core.

// ListIssueTemplates legge i modelli Markdown in .gitstack/ISSUE_TEMPLATE/ sul
// branch principale del repo (I11). Per ogni file .md estrae il front matter
// YAML (title, about, labels) e il body; un modello malformato viene saltato
// con un avviso, senza interrompere l'elenco. Se la cartella non esiste il
// risultato è {items: []}.
func (s *apiServer) ListIssueTemplates(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam) {
	a, ok := s.codeAccess(w, r, owner, name)
	if !ok {
		return
	}

	raw, err := a.git.ReadJSON(r.Context(), a.caller, a.repo.ID, "tree", url.Values{
		"ref":  {a.repo.DefaultBranch},
		"path": {".gitstack/ISSUE_TEMPLATE"},
	})
	if err != nil {
		var ae *gitclient.APIError
		if errors.As(err, &ae) && ae.Status == http.StatusNotFound &&
			(ae.Code == "not_found" || ae.Code == "ref_not_found") {
			writeJSON(w, http.StatusOK, openapi.IssueTemplateList{Items: []openapi.IssueTemplate{}})
			return
		}
		gitFailure(w, err)
		return
	}

	var tree openapi.Tree
	if err := json.Unmarshal(raw, &tree); err != nil {
		gitFailure(w, err)
		return
	}

	repoKey := a.repo.OwnerName + "/" + a.repo.Name
	readContents := func(path string) (string, error) {
		raw2, err := a.git.ReadJSON(r.Context(), a.caller, a.repo.ID, "contents", url.Values{
			"ref":  {a.repo.DefaultBranch},
			"path": {path},
		})
		if err != nil {
			slog.Warn("lettura del contenuto del modello non riuscita", "repo", repoKey, "file", path, "err", err)
			return "", gitclient.ErrNotFound
		}
		var fc openapi.FileContent
		if err := json.Unmarshal(raw2, &fc); err != nil {
			slog.Warn("parsing del contenuto del modello non riuscito", "repo", repoKey, "file", path, "err", err)
			return "", gitclient.ErrNotFound
		}
		if fc.Content == nil {
			return "", gitclient.ErrNotFound
		}
		return *fc.Content, nil
	}

	templates := listIssueTemplatesFromTree(repoKey, tree.Entries, readContents)
	writeJSON(w, http.StatusOK, openapi.IssueTemplateList{Items: templates})
}
