package httpapi

import (
	"net/http"

	"github.com/fathorMB/GitStack/services/identity/internal/openapi"
)

func (s *server) CheckPermission(w http.ResponseWriter, r *http.Request) {
	unavailable(w)
}

func (s *server) ListResourceGrants(w http.ResponseWriter, r *http.Request, resourceId openapi.ResourceIdParam, params openapi.ListResourceGrantsParams) {
	unavailable(w)
}

func (s *server) CreateResourceGrant(w http.ResponseWriter, r *http.Request, resourceId openapi.ResourceIdParam) {
	unavailable(w)
}

func (s *server) UpdateResourceGrant(w http.ResponseWriter, r *http.Request, resourceId openapi.ResourceIdParam, grantId openapi.GrantIdParam) {
	unavailable(w)
}

func (s *server) DeleteResourceGrant(w http.ResponseWriter, r *http.Request, resourceId openapi.ResourceIdParam, grantId openapi.GrantIdParam) {
	unavailable(w)
}

func (s *server) GetMyResourcePermission(w http.ResponseWriter, r *http.Request, resourceId openapi.ResourceIdParam) {
	unavailable(w)
}
