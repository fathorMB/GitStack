package httpserver

import (
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// Certificati dei domini propri (v3-c): il contratto è fissato (GIT-455), l'implementazione arriva
// con l'item del certificato. Fino ad allora 501, come per le altre operazioni ancora da realizzare.
// I domini veri e propri sono in apps_domains.go.

func (s *apiServer) UploadAppDomainCertificate(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, domain openapi.AppDomainParam) {
	notImplemented(w)
}

func (s *apiServer) RemoveAppDomainCertificate(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, domain openapi.AppDomainParam) {
	notImplemented(w)
}
