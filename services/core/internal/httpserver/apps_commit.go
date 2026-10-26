package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
)

// Commit dell'immagine (H14, GIT-386): l'etichetta OCI org.opencontainers.image.revision, letta
// dal servizio git per le immagini del registry del repo. Si legge una volta per deploy, fuori da
// ogni transazione, con un limite di tempo: il deploy non aspetta il servizio git più di così.
const imageCommitTimeout = 5 * time.Second

// imageCommit dà il commit dell'immagine, o nil: per un'immagine esterna, per un'etichetta assente
// o non valida, e se la lettura fallisce (allora il deploy va avanti e si scrive un log).
func (s *apiServer) imageCommit(ctx context.Context, repo store.Repo, img store.AppImage) *string {
	if img.Source != "registry" {
		return nil
	}
	image, tag, digest := s.registryParts(img.Reference, repo)
	if img.Digest != nil {
		digest = *img.Digest
	}
	if digest == "" && tag == "" {
		return nil
	}
	ir, ok := s.git.(gitclient.ImageRevisioner)
	if !ok {
		return nil
	}
	caller, ok := trust.FromContext(ctx)
	if !ok {
		slog.Default().Warn("commit dell'immagine: identità del chiamante assente, GITSTACK_COMMIT_SHA resta assente")
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, imageCommitTimeout)
	defer cancel()
	rev, err := ir.ImageRevision(ctx, caller, repo.ID, image, tag, digest)
	if err != nil {
		if !errors.Is(err, gitclient.ErrNotFound) {
			slog.Default().Warn("commit dell'immagine: lettura del registry non riuscita, GITSTACK_COMMIT_SHA resta assente", "err", err, "image", img.Reference)
		}
		return nil
	}
	if rev == "" {
		return nil
	}
	return &rev
}

// registryParts scompone un riferimento del registry del repo (quello che resolveAppImage ha già
// accettato) in nome dell'immagine sotto il repo, tag e digest. Le parti assenti sono «».
func (s *apiServer) registryParts(ref string, repo store.Repo) (image, tag, digest string) {
	base := s.registryHost() + "/" + strings.ToLower(repo.OwnerName) + "/" + strings.ToLower(repo.Name)
	if len(ref) < len(base) || !strings.EqualFold(ref[:len(base)], base) {
		return "", "", ""
	}
	rest := strings.TrimPrefix(ref[len(base):], "/")
	if i := strings.Index(rest, "@"); i >= 0 {
		digest = rest[i+1:]
		rest = rest[:i]
	}
	if i := strings.LastIndex(rest, ":"); i >= 0 && i > strings.LastIndex(rest, "/") {
		tag = rest[i+1:]
		rest = rest[:i]
	}
	return strings.TrimPrefix(rest, "/"), tag, digest
}
