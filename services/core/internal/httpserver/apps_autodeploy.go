package httpserver

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
)

// RegistryImagePushedPathPrefix è la rotta interna con cui il servizio git dice a core che un job
// della CI ha pubblicato un tag (H7, GIT-349).
const RegistryImagePushedPathPrefix = "/internal/registry/image-pushed/"

type imagePushedBody struct {
	Image   string `json:"image"`
	Tag     string `json:"tag"`
	Digest  string `json:"digest"`
	RunID   string `json:"runId"`
	Ref     string `json:"ref"`
	Event   string `json:"event"`
	ActorID string `json:"actorId"`
}

// registryImagePushed è il deploy automatico da branch (H7). Il servizio git (solo con la sua
// chiave) riporta il tag che un job ha pubblicato, con il run che lo ha causato. Per ogni app del
// repo che segue il branch del run, e usa la stessa immagine, core mette in produzione la nuova
// versione se e solo se:
//   - chi ha fatto il push ha ancora `write` sul repo (CI4: il deploy usa i suoi permessi);
//   - l'immagine è firmata dalla CI di questo repo (H10), non da altri e non da una chiave aggiuntiva;
//   - non è già quella in esecuzione e non c'è un altro deploy in corso.
//
// Se l'avvio fallisce il controller lascia la versione precedente (H12). Risponde sempre 204:
// il push dell'immagine non deve fallire per un deploy che non scatta.
func (s *apiServer) registryImagePushed(w http.ResponseWriter, r *http.Request) {
	if trust.CallerFromContext(r.Context()) != trust.CallerGit {
		writeError(w, http.StatusForbidden, "forbidden", "Il servizio chiamante non può fare questa operazione.")
		return
	}
	repoID, err := uuid.Parse(r.PathValue("repoId"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Repo non trovato.")
		return
	}
	var in imagePushedBody
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if !digestRe.MatchString(in.Digest) || in.Tag == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "Dati del push non validi.")
		return
	}
	s.autoDeploy(r, repoID, in)
	w.WriteHeader(http.StatusNoContent)
}

func (s *apiServer) autoDeploy(r *http.Request, repoID uuid.UUID, in imagePushedBody) {
	ctx := r.Context()
	log := slog.Default().With("repoId", repoID, "tag", in.Tag, "ref", in.Ref)
	branch, isBranch := strings.CutPrefix(in.Ref, "refs/heads/")
	actorID, aerr := uuid.Parse(in.ActorID)
	if !isBranch || branch == "" || (in.Event != "push" && in.Event != "workflow_dispatch") || aerr != nil {
		return // un tag, una PR o una schedule senza attore non fanno deploy
	}
	if s.appCtl == nil || s.repoIdentity == nil {
		return
	}
	repo, err := s.resources.GetRepoByID(ctx, repoID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && (repo.DeletedAt != nil || repo.ArchivedAt != nil)) {
		return
	}
	if err != nil {
		log.Error("deploy automatico: lettura del repo", "err", err)
		return
	}
	apps, err := s.resources.ListApps(ctx, repoID)
	if err != nil {
		log.Error("deploy automatico: elenco delle app", "err", err)
		return
	}
	var candidates []store.App
	for _, a := range apps {
		if a.FollowBranch == nil || *a.FollowBranch != branch || a.AutoDeployPaused || a.Image.Source != "registry" {
			continue
		}
		if image, _, _ := s.registryParts(a.Image.Reference, repo); image == in.Image {
			candidates = append(candidates, a)
		}
	}
	if len(candidates) == 0 {
		return
	}
	// CI4: i permessi sono di chi ha fatto il push.
	has, err := s.repoIdentity.HasRole(ctx, actorID, repo.ID, "write")
	if err != nil {
		log.Warn("deploy automatico: verifica del permesso non riuscita", "err", err)
		return
	}
	if !has {
		log.Warn("deploy automatico: chi ha fatto il push non ha più `write` sul repo, nessun deploy", "actor", actorID)
		return
	}
	actor := &store.AppActor{ID: actorID, Username: actorID.String(), Type: "human"}
	if dir, err := s.resolveUsers(ctx, []uuid.UUID{actorID}); err == nil {
		if u, ok := dir[actorID]; ok {
			actor.Username, actor.Type = u.Username, string(u.Kind)
		}
	}
	base := s.registryHost() + "/" + strings.ToLower(repo.OwnerName) + "/" + strings.ToLower(repo.Name)
	if in.Image != "" {
		base += "/" + in.Image
	}
	img := store.AppImage{Reference: base + ":" + in.Tag, Digest: &in.Digest, Source: "registry"}
	// Solo la firma della CI di questo repo (H10): né immagini non firmate, né di un altro repo, né
	// di una chiave fidata aggiuntiva.
	sig, err := s.checkSignature(ctx, repo, img, false)
	if err != nil {
		log.Warn("deploy automatico: verifica della firma non riuscita", "err", err)
		return
	}
	if !sig.ByCI || sig.Digest != in.Digest {
		log.Warn("deploy automatico: l'immagine non è firmata dalla CI di questo repo, nessun deploy")
		return
	}
	by := sig.SignedBy
	img.Verified, img.SignedBy = true, &by
	commit := s.imageCommit(ctx, repo, img)
	for _, a := range candidates {
		// Già l'immagine dell'ultima versione, anche se non è ancora partita (altro tag dello stesso build).
		if latest, _, err := s.resources.ListAppVersions(ctx, a.ID, 1, 0); err == nil && len(latest) == 1 && latest[0].Image.Digest != nil && *latest[0].Image.Digest == in.Digest {
			continue
		}
		_, ver, err := s.resources.BeginAppDeploy(ctx, repo.ID, a.Name, store.NewAppDeploy{
			Trigger: "branch_push", Image: img, Branch: &branch, Actor: actor, CommitSha: commit,
		}, s.now())
		if err != nil {
			// Un deploy già in corso, o l'app sparita nel frattempo: il prossimo push riprova.
			log.Warn("deploy automatico: deploy non avviato", "app", a.Name, "err", err)
			continue
		}
		s.appCtl.Kick()
		log.Info("deploy automatico avviato", "app", a.Name, "version", ver.Number, "actor", actor.Username)
	}
}
