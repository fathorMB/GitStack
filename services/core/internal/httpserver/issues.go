package httpserver

import (
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

	var tree openapi.Tree
	if !a.readJSON(w, r, "tree", url.Values{"ref": {a.repo.DefaultBranch}, "path": {".gitstack/ISSUE_TEMPLATE"}}, &tree) {
		// cartella assente o repo vuoto: il contratto dice items: []
		writeJSON(w, http.StatusOK, openapi.IssueTemplateList{Items: []openapi.IssueTemplate{}})
		return
	}

	readContents := func(path string) (string, error) {
		var fc openapi.FileContent
		if !a.readJSON(w, r, "contents", url.Values{"ref": {a.repo.DefaultBranch}, "path": {path}}, &fc) {
			return "", gitclient.ErrNotFound
		}
		if fc.Content == nil {
			return "", gitclient.ErrNotFound
		}
		return *fc.Content, nil
	}

	templates := listIssueTemplatesFromTree(tree.Path, tree.Entries, readContents)
	writeJSON(w, http.StatusOK, openapi.IssueTemplateList{Items: templates})
}
