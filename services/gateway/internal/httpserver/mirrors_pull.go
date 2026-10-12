package httpserver

import (
	"net/http"

	"github.com/fathorMB/GitStack/services/gateway/internal/openapi"
)

// Mirror in pull (MP1-MP8, GIT-195): serviti da core, il gateway li instrada
// con l'handler condiviso (le dichiarazioni di sicurezza vengono dal contratto).

func (s *apiServer) ImportRepository(w http.ResponseWriter, r *http.Request) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) GetRepoPullMirror(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) EnableRepoPullMirror(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) UpdateRepoPullMirror(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) SyncRepoPullMirror(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) PauseRepoPullMirror(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ResumeRepoPullMirror(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) StopRepoPullMirror(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ListRepoPullMirrorRuns(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.ListRepoPullMirrorRunsParams) {
	s.proxy.ServeHTTP(w, r)
}
