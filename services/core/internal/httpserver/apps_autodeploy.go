package httpserver

import (
	"errors"
	"log/slog"
	"net/http"
	"path"
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

// registryImagePushed è il deploy automatico da branch o da regola sui tag (H7). Il servizio git (solo con la sua
// chiave) riporta il tag che un job ha pubblicato, con il run che lo ha causato. Per ogni app del
// repo che segue il branch del run (o una regola sui tag che il tag rispetta), e usa la stessa immagine, core mette in produzione la nuova
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
	if (in.Event != "push" && in.Event != "workflow_dispatch") || aerr != nil {
		return // una PR o una schedule senza attore non fanno deploy
	}
	isBranch = isBranch && branch != ""
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
	// Ogni app segue un branch oppure una regola sui tag (mai tutte e due): il branch scatta per il
	// ref del run, la regola per il tag dell'immagine che arriva nel registry (H7 del 2026-10-26).
	type candidate struct {
		app     store.App
		trigger string
	}
	var candidates []candidate
	for _, a := range apps {
		if a.AutoDeployPaused || a.Image.Source != "registry" {
			continue
		}
		trigger := ""
		switch {
		case a.FollowBranch != nil && isBranch && *a.FollowBranch == branch:
			trigger = "branch_push"
		case a.FollowTagPattern != nil && tagMatches(*a.FollowTagPattern, in.Tag):
			trigger = "tag_push"
		default:
			continue
		}
		if image, _, _ := s.registryParts(a.Image.Reference, repo); image == in.Image {
			candidates = append(candidates, candidate{a, trigger})
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
	for _, c := range candidates {
		a := c.app
		// Già l'immagine dell'ultima versione, anche se non è ancora partita (altro tag dello stesso build).
		if latest, _, err := s.resources.ListAppVersions(ctx, a.ID, 1, 0); err == nil && len(latest) == 1 && latest[0].Image.Digest != nil && *latest[0].Image.Digest == in.Digest {
			continue
		}
		var versionBranch *string
		if c.trigger == "branch_push" {
			versionBranch = &branch
		}
		_, ver, err := s.resources.BeginAppDeploy(ctx, repo.ID, a.Name, store.NewAppDeploy{
			Trigger: c.trigger, Image: img, Branch: versionBranch, Actor: actor, CommitSha: commit,
		}, s.now())
		if err != nil {
			// Un deploy già in corso, o l'app sparita nel frattempo: il prossimo push riprova.
			log.Warn("deploy automatico: deploy non avviato", "app", a.Name, "err", err)
			continue
		}
		s.appCtl.Kick()
		log.Info("deploy automatico avviato", "app", a.Name, "trigger", c.trigger, "version", ver.Number, "actor", actor.Username)
	}
}

// maxFollowTagPatternLen è la lunghezza massima di una regola sui tag (come il vincolo del database).
const maxFollowTagPatternLen = 128

// validTagPattern controlla la regola sui tag (un glob di path.Match, per esempio v*-test.*): un
// messaggio d'errore per il campo, vuoto se va bene.
func validTagPattern(p string) string {
	if strings.TrimSpace(p) == "" || len(p) > maxFollowTagPatternLen {
		return "followTagPattern: una regola sui tag di 1-128 caratteri, per esempio v*-test.*."
	}
	if strings.ContainsAny(p, " /") {
		return "followTagPattern: i tag non hanno spazi né barre."
	}
	if _, err := path.Match(p, "x"); err != nil {
		return "followTagPattern: regola non valida (" + err.Error() + ")."
	}
	return ""
}

// tagMatches dice se il tag rispetta la regola (glob: * qualsiasi sequenza, ? un carattere, [..]
// una classe). L'intero tag deve combaciare: v*-test.* accetta v1.2-test.3 ma non v1.2.
func tagMatches(pattern, tag string) bool {
	ok, err := path.Match(pattern, tag)
	return err == nil && ok
}

// writeFollowBoth rifiuta branch e regola sui tag insieme: l'app sceglie una sola sorgente (H7).
func writeFollowBoth(w http.ResponseWriter) {
	writeError(w, http.StatusUnprocessableEntity, "follow_both", "Un'app segue un branch oppure una regola sui tag, non tutte e due: togli followBranch o followTagPattern (null) prima di impostare l'altro.")
}
