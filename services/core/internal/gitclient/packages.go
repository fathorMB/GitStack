package gitclient

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
)

// Pacchetti del registry OCI (GIT-361, RG1): core decide i permessi, il servizio git legge ed
// elimina nel registry. Le rotte sono /internal/git/repos/{repoId}/packages.

// PackagePusher è chi ha pubblicato (user, agent, job).
type PackagePusher struct {
	Kind  string `json:"kind"`
	Login string `json:"login"`
}

// PackageTag è un tag di un'immagine.
type PackageTag struct {
	Name      string         `json:"name"`
	Digest    string         `json:"digest"`
	SizeBytes int64          `json:"sizeBytes"`
	PushedAt  time.Time      `json:"pushedAt"`
	Signature string         `json:"signature"`
	HasSbom   bool           `json:"hasSbom"`
	Protected bool           `json:"protected"`
	PushedBy  *PackagePusher `json:"pushedBy,omitempty"`
	// Revision è il commit dell'etichetta OCI revision (H14, GIT-386): assente se non c'è.
	Revision string `json:"revision,omitempty"`
}

// RemovedPackageTag è un tag eliminato da una regola di pulizia.
type RemovedPackageTag struct {
	Name      string    `json:"name"`
	Digest    string    `json:"digest"`
	Rule      string    `json:"rule"`
	RemovedAt time.Time `json:"removedAt"`
}

// PackageInfo è un'immagine del repo; Image è il nome sotto il repo (vuoto = senza nome).
type PackageInfo struct {
	Image       string              `json:"image"`
	Tags        []string            `json:"tags"`
	TagDetails  []PackageTag        `json:"tagDetails"`
	Digest      string              `json:"digest"`
	SizeBytes   int64               `json:"sizeBytes"`
	UpdatedAt   time.Time           `json:"updatedAt"`
	Signature   string              `json:"signature"`
	HasSbom     bool                `json:"hasSbom"`
	Kind        string              `json:"kind"`
	PushedBy    *PackagePusher      `json:"pushedBy,omitempty"`
	RemovedTags []RemovedPackageTag `json:"removedTags,omitempty"`
}

// Packager è ciò che serve a core per elencare ed eliminare i pacchetti.
type Packager interface {
	ListPackages(ctx context.Context, caller trust.Identity, repoID uuid.UUID) ([]PackageInfo, error)
	// DeletePackage elimina un tag (tag non vuoto) o l'immagine intera. ErrNotFound se manca,
	// ErrConflict se un tag è protetto.
	DeletePackage(ctx context.Context, caller trust.Identity, repoID uuid.UUID, image, tag string) error
}

// ListPackages implementa Packager.
func (c *Client) ListPackages(ctx context.Context, caller trust.Identity, repoID uuid.UUID) ([]PackageInfo, error) {
	var out struct {
		Items []PackageInfo `json:"items"`
	}
	if err := c.do(ctx, caller, http.MethodGet, "/internal/git/repos/"+repoID.String()+"/packages", nil, &out, http.StatusOK); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// DeletePackage implementa Packager.
func (c *Client) DeletePackage(ctx context.Context, caller trust.Identity, repoID uuid.UUID, image, tag string) error {
	q := url.Values{}
	q.Set("image", image)
	if tag != "" {
		q.Set("tag", tag)
	}
	return c.do(ctx, caller, http.MethodDelete, "/internal/git/repos/"+repoID.String()+"/packages?"+q.Encode(), nil, nil, http.StatusNoContent)
}

var _ Packager = (*Client)(nil)
