package httpserver

import (
	"net/http"

	"github.com/fathorMB/GitStack/services/gateway/internal/openapi"
)

// Domini propri delle app pubbliche (v3-c, GIT-455): serviti da core; il gateway
// li inoltra per applicare la sicurezza generata dal contratto.

func (s *apiServer) ListAppDomains(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) AddAppDomain(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) GetAppDomain(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, domain openapi.AppDomainParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) RemoveAppDomain(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, domain openapi.AppDomainParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) RecheckAppDomain(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, domain openapi.AppDomainParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) UploadAppDomainCertificate(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, domain openapi.AppDomainParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) RemoveAppDomainCertificate(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, domain openapi.AppDomainParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) ListAdminPublicApps(w http.ResponseWriter, r *http.Request) {
	s.appsProxy(w, r)
}
