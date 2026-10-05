package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Letture del codice (M-04, GIT-80, GIT-84). Core è l'unico punto che applica
// i permessi: risolve owner/nome, verifica `read` (un repo non leggibile,
// eliminato o inesistente dà la stessa 404 senza dati), poi chiama il
// servizio git. Raw, archivi e .diff/.patch passano in streaming.

// codeAccess è il risultato del controllo d'accesso di una lettura.
type codeAccess struct {
	caller trust.Identity
	repo   store.Repo
	git    gitclient.Reader
}

func (s *apiServer) codeAccess(w http.ResponseWriter, r *http.Request, owner, name string) (codeAccess, bool) {
	caller, userID, ok := callerIdentity(w, r)
	if !ok {
		return codeAccess{}, false
	}
	if !s.reposReady(w) {
		return codeAccess{}, false
	}
	repo, ok := s.lookupReadable(w, r, userID, owner, name)
	if !ok {
		return codeAccess{}, false
	}
	rd, ok := s.git.(gitclient.Reader)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "git_unavailable", "Letture del codice non disponibili.")
		return codeAccess{}, false
	}
	return codeAccess{caller: caller, repo: repo, git: rd}, true
}

// gitFailure traduce un errore del servizio git.
func gitFailure(w http.ResponseWriter, err error) {
	var ae *gitclient.APIError
	if errors.As(err, &ae) {
		msg := ae.Message
		if msg == "" {
			msg = "Richiesta non soddisfatta."
		}
		writeError(w, ae.Status, ae.Code, msg)
		return
	}
	slog.Default().Warn("lettura del codice non riuscita", "err", err)
	writeError(w, http.StatusServiceUnavailable, "git_unavailable", "Servizio git non disponibile.")
}

// refOrDefault: senza ref vale il branch principale (R4).
func (a codeAccess) refOrDefault(ref *string) string {
	if ref != nil && *ref != "" {
		return *ref
	}
	return a.repo.DefaultBranch
}

func setIf(q url.Values, key string, v *string) {
	if v != nil && *v != "" {
		q.Set(key, *v)
	}
}

// readJSON chiama git e decodifica la risposta in out.
func (a codeAccess) readJSON(w http.ResponseWriter, r *http.Request, path string, q url.Values, out any) bool {
	raw, err := a.git.ReadJSON(r.Context(), a.caller, a.repo.ID, path, q)
	if err != nil {
		gitFailure(w, err)
		return false
	}
	if err := json.Unmarshal(raw, out); err != nil {
		slog.Default().Warn("risposta di git non valida", "path", path, "err", err)
		writeError(w, http.StatusServiceUnavailable, "git_unavailable", "Servizio git non disponibile.")
		return false
	}
	return true
}

// linkAuthors collega autore e committer all'utente GitStack con quella
// email (tipo human/agent per il badge). Se identity non risponde le letture
// restano valide, senza `user`.
func (s *apiServer) linkAuthors(ctx context.Context, commits ...*openapi.CommitSummary) {
	look, ok := s.repoIdentity.(identityclient.EmailLookup)
	if !ok || len(commits) == 0 {
		return
	}
	seen := map[string]bool{}
	var emails []string
	for _, c := range commits {
		for _, p := range []*openapi.CommitPerson{&c.Author, &c.Committer} {
			if e := strings.ToLower(p.Email); e != "" && !seen[e] {
				seen[e] = true
				emails = append(emails, e)
			}
		}
	}
	if len(emails) == 0 {
		return
	}
	users, err := look.LookupEmails(ctx, emails)
	if err != nil {
		slog.Default().Warn("autori dei commit non collegati agli utenti", "err", err)
		return
	}
	for _, c := range commits {
		for _, p := range []*openapi.CommitPerson{&c.Author, &c.Committer} {
			if u, ok := users[strings.ToLower(p.Email)]; ok {
				p.User = &openapi.CodeUser{
					Id: openapi_types.UUID(u.ID), Username: u.Username,
					Kind: openapi.CodeUserKind(u.Kind), AvatarUrl: u.AvatarURL,
				}
			}
		}
	}
}

func (s *apiServer) GetRepositoryTree(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, params openapi.GetRepositoryTreeParams) {
	a, ok := s.codeAccess(w, r, owner, name)
	if !ok {
		return
	}
	q := url.Values{"ref": {a.refOrDefault(params.Ref)}}
	setIf(q, "path", params.Path)
	var out openapi.Tree
	if !a.readJSON(w, r, "tree", q, &out) {
		return
	}
	commits := make([]*openapi.CommitSummary, len(out.Entries))
	for i := range out.Entries {
		commits[i] = &out.Entries[i].LastCommit
	}
	s.linkAuthors(r.Context(), commits...)
	writeJSON(w, http.StatusOK, out)
}

func (s *apiServer) GetRepositoryFile(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, params openapi.GetRepositoryFileParams) {
	a, ok := s.codeAccess(w, r, owner, name)
	if !ok {
		return
	}
	q := url.Values{"ref": {a.refOrDefault(params.Ref)}, "path": {params.Path}}
	var out openapi.FileContent
	if !a.readJSON(w, r, "contents", q, &out) {
		return
	}
	s.linkAuthors(r.Context(), &out.LastCommit)
	writeJSON(w, http.StatusOK, out)
}

func (s *apiServer) GetRepositoryReadme(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, params openapi.GetRepositoryReadmeParams) {
	a, ok := s.codeAccess(w, r, owner, name)
	if !ok {
		return
	}
	q := url.Values{"ref": {a.refOrDefault(params.Ref)}}
	setIf(q, "path", params.Path)
	var out openapi.FileContent
	if !a.readJSON(w, r, "readme", q, &out) {
		return
	}
	s.linkAuthors(r.Context(), &out.LastCommit)
	writeJSON(w, http.StatusOK, out)
}

func (s *apiServer) GetRepositoryBranches(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam) {
	a, ok := s.codeAccess(w, r, owner, name)
	if !ok {
		return
	}
	var out openapi.BranchList
	if !a.readJSON(w, r, "branches", nil, &out) {
		return
	}
	commits := make([]*openapi.CommitSummary, len(out.Items))
	for i := range out.Items {
		b := &out.Items[i]
		// Il servizio git non conosce la protezione (R9): la imposta core.
		b.Protected = b.Name == a.repo.DefaultBranch && a.repo.ProtectDefaultBranch
		commits[i] = &b.Commit
	}
	s.linkAuthors(r.Context(), commits...)
	writeJSON(w, http.StatusOK, out)
}

func (s *apiServer) GetRepositoryTags(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam) {
	a, ok := s.codeAccess(w, r, owner, name)
	if !ok {
		return
	}
	var out openapi.TagList
	if !a.readJSON(w, r, "tags", nil, &out) {
		return
	}
	commits := make([]*openapi.CommitSummary, len(out.Items))
	base := "/repos/" + url.PathEscape(a.repo.OwnerName) + "/" + url.PathEscape(a.repo.Name) + "/archive?ref="
	for i := range out.Items {
		t := &out.Items[i]
		ref := url.QueryEscape(t.Name)
		zip, tgz := base+ref+"&format=zip", base+ref+"&format=tar.gz"
		t.ZipUrl, t.TarGzUrl = &zip, &tgz
		commits[i] = &t.Commit
	}
	s.linkAuthors(r.Context(), commits...)
	writeJSON(w, http.StatusOK, out)
}

func (s *apiServer) GetRepositoryCommits(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, params openapi.GetRepositoryCommitsParams) {
	a, ok := s.codeAccess(w, r, owner, name)
	if !ok {
		return
	}
	q := url.Values{"ref": {a.refOrDefault(params.Ref)}}
	setIf(q, "author", params.Author)
	setIf(q, "path", params.Path)
	if params.Page != nil {
		q.Set("page", strconv.Itoa(*params.Page))
	}
	if params.PerPage != nil {
		q.Set("perPage", strconv.Itoa(*params.PerPage))
	}
	var out openapi.CommitList
	if !a.readJSON(w, r, "commits", q, &out) {
		return
	}
	commits := make([]*openapi.CommitSummary, len(out.Items))
	for i := range out.Items {
		commits[i] = &out.Items[i]
	}
	s.linkAuthors(r.Context(), commits...)
	writeJSON(w, http.StatusOK, out)
}

func (s *apiServer) GetRepositoryCommit(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, sha openapi.CommitShaParam, params openapi.GetRepositoryCommitParams) {
	a, ok := s.codeAccess(w, r, owner, name)
	if !ok {
		return
	}
	q := url.Values{}
	if params.IgnoreWhitespace != nil && *params.IgnoreWhitespace {
		q.Set("ignoreWhitespace", "true")
	}
	var out openapi.CommitDetail
	if !a.readJSON(w, r, "commits/"+url.PathEscape(sha), q, &out) {
		return
	}
	if params.Path != nil && *params.Path != "" {
		i := slices.IndexFunc(out.Files, func(f openapi.FileDiff) bool { return f.Path == *params.Path })
		if i < 0 {
			writeError(w, http.StatusNotFound, "not_found", "Percorso non trovato nel commit.")
			return
		}
		out.Files = []openapi.FileDiff{out.Files[i]}
	}
	s.linkAuthors(r.Context(), &out.Commit)
	writeJSON(w, http.StatusOK, out)
}

func (s *apiServer) GetRepositoryBlame(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, params openapi.GetRepositoryBlameParams) {
	a, ok := s.codeAccess(w, r, owner, name)
	if !ok {
		return
	}
	q := url.Values{"ref": {a.refOrDefault(params.Ref)}, "path": {params.Path}}
	var out openapi.Blame
	if !a.readJSON(w, r, "blame", q, &out) {
		return
	}
	commits := make([]*openapi.CommitSummary, len(out.Ranges))
	for i := range out.Ranges {
		commits[i] = &out.Ranges[i].Commit
	}
	s.linkAuthors(r.Context(), commits...)
	writeJSON(w, http.StatusOK, out)
}

// forward inoltra una risposta di git in streaming, con le intestazioni che
// decide il servizio git (tipo, disposizione, sicurezza B3). In ogni caso
// nosniff e sandbox vengono garantiti anche qui.
func (a codeAccess) forward(w http.ResponseWriter, r *http.Request, path string, q url.Values) {
	st, err := a.git.OpenStream(r.Context(), a.caller, a.repo.ID, path, q)
	if err != nil {
		gitFailure(w, err)
		return
	}
	defer func() { _ = st.Body.Close() }()
	for _, h := range []string{"Content-Type", "Content-Length", "Content-Disposition"} {
		if v := st.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
	// Mai pagine eseguibili (B3): qualunque cosa git dichiari, html e svg no.
	if ct := strings.ToLower(w.Header().Get("Content-Type")); strings.HasPrefix(ct, "text/html") || strings.HasPrefix(ct, "image/svg") || strings.Contains(ct, "xhtml") {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="download"`)
	}
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, st.Body); err != nil {
		slog.Default().Warn("streaming interrotto", "path", path, "err", err)
		// Le intestazioni sono già partite: si interrompe la risposta, così
		// il client non scambia un file tronco per completo.
		panic(http.ErrAbortHandler)
	}
}

func (s *apiServer) GetRepositoryRaw(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, params openapi.GetRepositoryRawParams) {
	a, ok := s.codeAccess(w, r, owner, name)
	if !ok {
		return
	}
	a.forward(w, r, "raw", url.Values{"ref": {a.refOrDefault(params.Ref)}, "path": {params.Path}})
}

func (s *apiServer) GetRepositoryArchive(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, params openapi.GetRepositoryArchiveParams) {
	a, ok := s.codeAccess(w, r, owner, name)
	if !ok {
		return
	}
	q := url.Values{"ref": {a.refOrDefault(params.Ref)}, "name": {a.repo.Name}}
	if params.Format != nil {
		q.Set("format", string(*params.Format))
	}
	a.forward(w, r, "archive", q)
}

func (s *apiServer) GetRepositoryCommitPatch(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, sha openapi.CommitShaParam, params openapi.GetRepositoryCommitPatchParams) {
	a, ok := s.codeAccess(w, r, owner, name)
	if !ok {
		return
	}
	kind := "patch"
	if params.Format != nil && *params.Format == "diff" {
		kind = "diff"
	}
	q := url.Values{}
	if params.IgnoreWhitespace != nil && *params.IgnoreWhitespace {
		q.Set("ignoreWhitespace", "true")
	}
	a.forward(w, r, "commits/"+url.PathEscape(sha)+"/"+kind, q)
}

// GetRepositoryRawByPath è /repos/{owner}/{repo}/raw/{refAndPath...}: il ref
// può contenere `/`, quindi si prende il prefisso più lungo che è un branch,
// altrimenti un tag; se nessuno corrisponde, il primo segmento è uno sha.
func (s *apiServer) GetRepositoryRawByPath(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, refAndPath openapi.RefAndPathParam) {
	a, ok := s.codeAccess(w, r, owner, name)
	if !ok {
		return
	}
	refAndPath = strings.TrimLeft(refAndPath, "/")
	var branches openapi.BranchList
	var tags openapi.TagList
	if !a.readJSON(w, r, "branches", nil, &branches) || !a.readJSON(w, r, "tags", nil, &tags) {
		return
	}
	var names []string
	for _, b := range branches.Items {
		names = append(names, b.Name)
	}
	for _, t := range tags.Items {
		names = append(names, t.Name)
	}
	ref, path := splitRefAndPath(refAndPath, names)
	if ref == "" || path == "" {
		writeError(w, http.StatusNotFound, "ref_not_found", "Ref o percorso non trovato.")
		return
	}
	a.forward(w, r, "raw", url.Values{"ref": {ref}, "path": {path}})
}

// splitRefAndPath: prefisso più lungo fra i nomi noti (i branch prima dei tag
// a pari lunghezza, perché names li elenca in quest'ordine); altrimenti il
// primo segmento come sha.
func splitRefAndPath(s string, names []string) (string, string) {
	best := ""
	for _, n := range names {
		if len(n) > len(best) && strings.HasPrefix(s, n+"/") {
			best = n
		}
	}
	if best != "" {
		return best, strings.TrimPrefix(s, best+"/")
	}
	ref, path, _ := strings.Cut(s, "/")
	return ref, path
}

func (s *apiServer) GetRepositoryLanguages(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.GetRepositoryLanguagesParams) {
	// Arriva con GIT-83 (handler interno gitGetLanguages).
	codeNotImplemented(w)
}

func codeNotImplemented(w http.ResponseWriter) {
	writeError(w, http.StatusNotImplemented, "not_implemented", "Lettura del codice non ancora disponibile.")
}

// ListRepositoryFiles: percorsi di tutti i file del ref per "Go to file" (B5).
func (s *apiServer) ListRepositoryFiles(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, params openapi.ListRepositoryFilesParams) {
	a, ok := s.codeAccess(w, r, owner, name)
	if !ok {
		return
	}
	q := url.Values{"ref": {a.refOrDefault(params.Ref)}}
	var out openapi.FileList
	if !a.readJSON(w, r, "files", q, &out) {
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// SearchRepositoryCode: ricerca testuale nel ref, senza indice (B5). Il testo
// cercato passa a git come parametro di query: lì è sempre una stringa fissa.
func (s *apiServer) SearchRepositoryCode(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, params openapi.SearchRepositoryCodeParams) {
	a, ok := s.codeAccess(w, r, owner, name)
	if !ok {
		return
	}
	q := url.Values{"ref": {a.refOrDefault(params.Ref)}, "q": {params.Q}}
	var out openapi.CodeSearchResult
	if !a.readJSON(w, r, "search", q, &out) {
		return
	}
	writeJSON(w, http.StatusOK, out)
}
