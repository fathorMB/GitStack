package registry

import (
	"context"
	"time"
)

// Deploy automatico da branch (H7, GIT-349): quando un job della CI pubblica un tag, il registry
// lo dice a core, che decide se qualche app segue il branch del run. Il registry non decide
// niente: riporta ciò che il ci-gateway ha detto del token del job.

// JobAccessRun è il run che sta pubblicando, come lo dà il ci-gateway.
type JobAccessRun struct {
	RunID, Ref, Event, ActorID string
}

// ImagePush è un tag pubblicato da un job.
type ImagePush struct {
	// Image è il nome sotto il repo (vuoto = senza nome).
	Image  string `json:"image"`
	Tag    string `json:"tag"`
	Digest string `json:"digest"`
	RunID  string `json:"runId"`
	// Ref è il ref del run (es. refs/heads/main), Event il suo evento, ActorID chi l'ha causato.
	Ref     string `json:"ref"`
	Event   string `json:"event"`
	ActorID string `json:"actorId"`
}

// PushNotifier è implementato dal backend che sa parlare con core: senza, il registry non
// notifica niente e il deploy automatico non scatta.
type PushNotifier interface {
	ImagePushed(ctx context.Context, repoID string, push ImagePush) error
}

// notifyImagePushed riporta a core il tag pubblicato da un job. Un errore non fa fallire il push:
// si registra e basta (l'app si può sempre aggiornare a mano).
func (h *Handler) notifyImagePushed(ctx context.Context, g *authzGrant, image, tag, digest string) {
	if h.notifier == nil || g.job.Ref == "" || tag == "" || !tagRe.MatchString(tag) {
		return
	}
	if _, aux := provenanceSubject(tag); aux {
		return // firma, SBOM e simili non sono immagini
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	err := h.notifier.ImagePushed(ctx, g.repoID, ImagePush{Image: image, Tag: tag, Digest: digest,
		RunID: g.job.RunID, Ref: g.job.Ref, Event: g.job.Event, ActorID: g.job.ActorID})
	if err != nil {
		h.log.Warn("deploy automatico: core non ha accettato la notifica del push", "err", err, "repoId", g.repoID, "tag", tag)
	}
}
