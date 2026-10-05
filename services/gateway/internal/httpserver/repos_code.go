package httpserver

import (
	"net/http"

	"github.com/fathorMB/GitStack/services/gateway/internal/openapi"
)

// Letture del codice (M-04): servite da core, il gateway le instrada con
// l'handler condiviso e non parla mai col servizio git.

func (s *apiServer) GetRepositoryArchive(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.GetRepositoryArchiveParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) GetRepositoryBlame(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.GetRepositoryBlameParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) GetRepositoryBranches(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) GetRepositoryCommits(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.GetRepositoryCommitsParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) GetRepositoryCommit(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.CommitShaParam, _ openapi.GetRepositoryCommitParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) GetRepositoryFile(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.GetRepositoryFileParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) GetRepositoryLanguages(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.GetRepositoryLanguagesParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) GetRepositoryRaw(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.GetRepositoryRawParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) GetRepositoryReadme(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.GetRepositoryReadmeParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) GetRepositoryTags(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) GetRepositoryTree(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.GetRepositoryTreeParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) GetRepositoryRawByPath(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.RefAndPathParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ListRepositoryFiles(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.ListRepositoryFilesParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) SearchRepositoryCode(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.SearchRepositoryCodeParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) GetRepositoryCommitPatch(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.CommitShaParam, _ openapi.GetRepositoryCommitPatchParams) {
	s.proxy.ServeHTTP(w, r)
}
