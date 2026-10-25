package httpserver

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Pacchetti OCI del repo (GIT-361, RG1). I permessi li decide core (ruolo read per vedere,
// admin per eliminare, repo non leggibile = 404); l'elenco e l'eliminazione li fa il registry
// del servizio git, con la sua API interna firmata (F9): mai il disco.

func (s *apiServer) packager(w http.ResponseWriter) (gitclient.Packager, bool) {
	p, ok := s.git.(gitclient.Packager)
	if !ok || s.git == nil {
		writeError(w, http.StatusServiceUnavailable, "git_unavailable", "Servizio git non configurato.")
		return nil, false
	}
	return p, true
}

func pusherToAPI(p *gitclient.PackagePusher) *openapi.PackagePusher {
	if p == nil {
		return nil
	}
	return &openapi.PackagePusher{Kind: openapi.PackagePusherKind(p.Kind), Login: p.Login}
}

func packageToAPI(repo store.Repo, p gitclient.PackageInfo) openapi.Package {
	name := repo.OwnerName + "/" + repo.Name
	if p.Image != "" {
		name += "/" + p.Image
	}
	tags := p.Tags
	if tags == nil {
		tags = []string{}
	}
	details := make([]openapi.PackageTag, 0, len(p.TagDetails))
	for _, t := range p.TagDetails {
		details = append(details, openapi.PackageTag{
			Name: t.Name, Digest: t.Digest, SizeBytes: t.SizeBytes, PushedAt: t.PushedAt,
			Signature: openapi.PackageTagSignature(t.Signature), HasSbom: t.HasSbom, Protected: t.Protected,
			PushedBy: pusherToAPI(t.PushedBy),
		})
	}
	removed := make([]openapi.RemovedPackageTag, 0, len(p.RemovedTags))
	for _, t := range p.RemovedTags {
		removed = append(removed, openapi.RemovedPackageTag{Name: t.Name, Digest: t.Digest, Rule: t.Rule, RemovedAt: t.RemovedAt})
	}
	out := openapi.Package{
		Name: name, Tags: tags, TagDetails: &details, SizeBytes: p.SizeBytes, UpdatedAt: p.UpdatedAt,
		Signature: openapi.PackageSignature(p.Signature), HasSbom: p.HasSbom, PushedBy: pusherToAPI(p.PushedBy),
		RemovedTags: &removed,
	}
	if p.Digest != "" {
		d := p.Digest
		out.Digest = &d
	}
	if p.Kind != "" {
		k := openapi.PackageKind(p.Kind)
		out.Kind = &k
	}
	return out
}

// ListRepositoryPackages implementa GET /repos/{owner}/{repo}/packages: chi legge il repo.
func (s *apiServer) ListRepositoryPackages(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam) {
	caller, userID, ok := callerIdentity(w, r)
	if !ok {
		return
	}
	if s.repoIdentity == nil {
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non configurata: impossibile verificare i permessi.")
		return
	}
	repo, ok := s.lookupReadable(w, r, userID, o, n)
	if !ok {
		return
	}
	pk, ok := s.packager(w)
	if !ok {
		return
	}
	items, err := pk.ListPackages(r.Context(), caller, repo.ID)
	if err != nil {
		s.packagesGitErr(w, err)
		return
	}
	out := openapi.PackageList{Items: make([]openapi.Package, 0, len(items))}
	for _, p := range items {
		out.Items = append(out.Items, packageToAPI(repo, p))
	}
	writeJSON(w, http.StatusOK, out)
}

// DeleteRepositoryPackage implementa DELETE /repos/{owner}/{repo}/packages: solo admin (RG1).
// La rotta chiede write:resource, quindi il ruolo admin lo verifica packageRepo (write → 403).
func (s *apiServer) DeleteRepositoryPackage(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, p openapi.DeleteRepositoryPackageParams) {
	repo, _, ok := s.packageRepo(w, r, o, n, true)
	if !ok {
		return
	}
	caller, _, _ := callerIdentity(w, r)
	pk, ok := s.packager(w)
	if !ok {
		return
	}
	// Il nome è «owner/repo[/immagine]», come lo dà l'elenco.
	prefix := repo.OwnerName + "/" + repo.Name
	var image string
	switch {
	case strings.EqualFold(p.Name, prefix):
	case len(p.Name) > len(prefix) && strings.EqualFold(p.Name[:len(prefix)+1], prefix+"/"):
		image = p.Name[len(prefix)+1:]
	default:
		writeError(w, http.StatusNotFound, "not_found", "Pacchetto non trovato.")
		return
	}
	tag := ""
	if p.Tag != nil {
		tag = *p.Tag
	}
	if err := pk.DeletePackage(r.Context(), caller, repo.ID, image, tag); err != nil {
		s.packagesGitErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *apiServer) packagesGitErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, gitclient.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Pacchetto non trovato.")
	case errors.Is(err, gitclient.ErrConflict):
		writeError(w, http.StatusConflict, "protected_tag", "Il tag è protetto: non si elimina.")
	case errors.Is(err, gitclient.ErrInvalid):
		writeError(w, http.StatusNotFound, "not_found", "Pacchetto non trovato.")
	default:
		slog.Default().Error("pacchetti: servizio git non disponibile", "err", err)
		writeError(w, http.StatusServiceUnavailable, "git_unavailable", "Servizio git non disponibile.")
	}
}
