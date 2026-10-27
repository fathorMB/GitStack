package apps

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fathorMB/GitStack/services/core/internal/apppull"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// annotationPullIssued porta nel Secret il momento in cui core ha emesso il token: serve a
// ruotarlo prima della scadenza (apppull.RefreshAfter) senza rifare il deploy.
const annotationPullIssued = "gitstack.io/pull-issued"

// pullCheckEvery: ogni quanto syncApp guarda se il pull secret di un'app va ruotato.
const pullCheckEvery = 10 * time.Minute

// PullCredentialsFor ritorna la Config.PullCredentials di core (GIT-425): una credenziale per app,
// di sola lettura sul registry del repo dell'app, firmata con la chiave del gateway e senza alcun
// utente dietro. Il server (l'host del registry) lo mette il controller dall'immagine.
// keys vuote: nessuna credenziale (nessun pull secret).
func PullCredentialsFor(keys []string, now func() time.Time) func(context.Context, store.App) (*PullCredential, error) {
	return func(_ context.Context, app store.App) (*PullCredential, error) {
		if len(keys) == 0 {
			return nil, nil
		}
		tok, err := apppull.Mint(keys, app.ID, now())
		if err != nil {
			return nil, err
		}
		return &PullCredential{Username: apppull.Username, Password: tok}, nil
	}
}

// imageHost è l'host (con porta) di un riferimento di immagine; vuoto se non ha host.
func imageHost(ref string) string {
	host, _, ok := strings.Cut(ref, "/")
	if ok && (strings.ContainsAny(host, ".:") || host == "localhost") {
		return host
	}
	return ""
}

// refreshPullSecret ruota il token del pull secret di un'app in servizio prima che scada: i pod
// che ripartono (nodo cambiato, riavvio) lo leggono dal Secret. Controlla al massimo ogni
// pullCheckEvery per app e solo per le immagini del registry del repo.
func (c *Controller) refreshPullSecret(ctx context.Context, app store.App) {
	if c.cfg.PullCredentials == nil {
		return
	}
	now := c.cfg.Now()
	c.mu.Lock()
	if c.pullChecked == nil {
		c.pullChecked = map[uuid.UUID]time.Time{}
	}
	if last, ok := c.pullChecked[app.ID]; ok && now.Sub(last) < pullCheckEvery {
		c.mu.Unlock()
		return
	}
	c.pullChecked[app.ID] = now
	c.mu.Unlock()

	v, err := c.st.GetAppVersion(ctx, app.ID, app.ActiveVersion)
	if err != nil || v.Image.Source != "registry" {
		return
	}
	ns := Namespace(app.ID)
	sec, err := c.kube.CoreV1().Secrets(ns).Get(ctx, pullSecretName, metav1.GetOptions{})
	switch {
	case err == nil:
		if at, perr := time.Parse(time.RFC3339, sec.Annotations[annotationPullIssued]); perr == nil && now.Sub(at) < apppull.RefreshAfter {
			return
		}
	case !apierrors.IsNotFound(err):
		return
	}
	cred, err := c.cfg.PullCredentials(ctx, app)
	if err != nil || cred == nil {
		return
	}
	if cred.Server == "" {
		cred.Server = imageHost(v.Image.Reference)
	}
	if err := c.ensurePullSecret(ctx, ns, app.ID, cred); err != nil {
		c.cfg.Log.Warn("apps: pull secret non ruotato", "app", app.ID, "err", err)
		return
	}
	c.cfg.Log.Info("apps: pull secret del registry ruotato", "app", app.ID)
}
