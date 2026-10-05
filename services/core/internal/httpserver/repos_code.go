package httpserver

import (
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// Letture del codice (M-04, GIT-80): il contratto e' fissato, gli handler
// arrivano con GIT-84 (B1, B3, B5, B6, B7: GIT-91). Fino ad allora rispondono 501.

func codeNotImplemented(w http.ResponseWriter) {
	writeError(w, http.StatusNotImplemented, "not_implemented", "Lettura del codice non ancora disponibile.")
}

func (s *apiServer) GetRepositoryArchive(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.GetRepositoryArchiveParams) {
	codeNotImplemented(w)
}

func (s *apiServer) GetRepositoryBlame(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.GetRepositoryBlameParams) {
	codeNotImplemented(w)
}

func (s *apiServer) GetRepositoryBranches(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	codeNotImplemented(w)
}

func (s *apiServer) GetRepositoryCommits(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.GetRepositoryCommitsParams) {
	codeNotImplemented(w)
}

func (s *apiServer) GetRepositoryCommit(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.CommitShaParam, _ openapi.GetRepositoryCommitParams) {
	codeNotImplemented(w)
}

func (s *apiServer) GetRepositoryFile(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.GetRepositoryFileParams) {
	codeNotImplemented(w)
}

func (s *apiServer) GetRepositoryLanguages(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.GetRepositoryLanguagesParams) {
	codeNotImplemented(w)
}

func (s *apiServer) GetRepositoryRaw(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.GetRepositoryRawParams) {
	codeNotImplemented(w)
}

func (s *apiServer) GetRepositoryReadme(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.GetRepositoryReadmeParams) {
	codeNotImplemented(w)
}

func (s *apiServer) GetRepositoryTags(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	codeNotImplemented(w)
}

func (s *apiServer) GetRepositoryTree(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.GetRepositoryTreeParams) {
	codeNotImplemented(w)
}

func (s *apiServer) GetRepositoryRawByPath(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.RefAndPathParam) {
	codeNotImplemented(w)
}

func (s *apiServer) ListRepositoryFiles(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.ListRepositoryFilesParams) {
	codeNotImplemented(w)
}

func (s *apiServer) SearchRepositoryCode(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.SearchRepositoryCodeParams) {
	codeNotImplemented(w)
}

func (s *apiServer) GetRepositoryCommitPatch(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.CommitShaParam, _ openapi.GetRepositoryCommitPatchParams) {
	codeNotImplemented(w)
}
