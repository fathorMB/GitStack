package httpserver

import (
	"net/http"

	"github.com/fathorMB/GitStack/services/gateway/internal/openapi"
)

// Mirror in push (V8, GIT-179): serviti da core, il gateway li instrada con
// l'handler condiviso (le dichiarazioni di sicurezza vengono dal contratto).

func (s *apiServer) ListRepoMirrors(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.ListRepoMirrorsParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) CreateRepoMirror(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) DeleteRepoMirror(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.MirrorIdParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) GetRepoMirror(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.MirrorIdParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) UpdateRepoMirror(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.MirrorIdParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ListRepoMirrorRuns(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.MirrorIdParam, _ openapi.ListRepoMirrorRunsParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) SyncRepoMirror(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.MirrorIdParam) {
	s.proxy.ServeHTTP(w, r)
}
