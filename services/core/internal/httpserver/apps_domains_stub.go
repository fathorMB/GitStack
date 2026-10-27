package httpserver

import (
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// Domini propri delle app pubbliche (v3-c, GIT-455): il contratto e' fissato,
// l'implementazione arriva negli item successivi. Fino ad allora 501, come
// per le altre operazioni delle app ancora da realizzare.

func (s *apiServer) ListAppDomains(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	notImplemented(w)
}

func (s *apiServer) AddAppDomain(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	notImplemented(w)
}

func (s *apiServer) GetAppDomain(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, domain openapi.AppDomainParam) {
	notImplemented(w)
}

func (s *apiServer) RemoveAppDomain(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, domain openapi.AppDomainParam) {
	notImplemented(w)
}

func (s *apiServer) RecheckAppDomain(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, domain openapi.AppDomainParam) {
	notImplemented(w)
}

func (s *apiServer) UploadAppDomainCertificate(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, domain openapi.AppDomainParam) {
	notImplemented(w)
}

func (s *apiServer) RemoveAppDomainCertificate(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, domain openapi.AppDomainParam) {
	notImplemented(w)
}
