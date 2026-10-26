package apps

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Volumi persistenti (v3-b, GIT-356, H3): i PVC dell'app nascono prima del Deployment che li monta
// e si tolgono solo quando nessuna versione in servizio li usa più.

// ensureVolumes crea i PVC dei volumi della versione che ancora non esistono. Un PVC esistente non
// si tocca: la dimensione è fissa e i dati ci stanno dentro.
func (c *Controller) ensureVolumes(ctx context.Context, app store.App, v store.AppVersion) error {
	pvcs := c.kube.CoreV1().PersistentVolumeClaims(Namespace(app.ID))
	for _, vol := range v.Config.Volumes {
		_, err := pvcs.Get(ctx, PVCName(vol.Name), metav1.GetOptions{})
		if err == nil {
			continue
		}
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("volume %s: %w", vol.Name, err)
		}
		if _, err := pvcs.Create(ctx, BuildPVC(app, vol), metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("volume %s: %w", vol.Name, err)
		}
	}
	return nil
}

// pruneVolumes elimina i PVC (e i dati) dei volumi tolti dall'app. Un PVC si tiene finché il modello
// o la versione in servizio lo nominano: se il deploy che lo toglie fallisce, la versione precedente
// lo monta ancora. Agisce solo sui PVC con la label del controller.
func (c *Controller) pruneVolumes(ctx context.Context, app store.App) {
	ns := Namespace(app.ID)
	pvcs := c.kube.CoreV1().PersistentVolumeClaims(ns)
	list, err := pvcs.List(ctx, metav1.ListOptions{LabelSelector: LabelVolume + "," + LabelManaged + "=true"})
	if err != nil || len(list.Items) == 0 {
		return
	}
	keep, err := c.keptVolumes(ctx, app)
	if err != nil {
		return
	}
	for _, p := range list.Items {
		if keep[p.Name] {
			continue
		}
		if err := pvcs.Delete(ctx, p.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			c.cfg.Log.Warn("apps: volume tolto non eliminato", "app", app.ID, "pvc", p.Name, "err", err)
			continue
		}
		c.cfg.Log.Info("apps: volume eliminato", "app", app.ID, "pvc", p.Name)
		c.platform(ctx, app.ID, fmt.Sprintf("volume %s eliminato con i suoi dati", p.Name[len("vol-"):]))
	}
}

// keptVolumes sono i nomi dei PVC da tenere: i volumi attuali dell'app e quelli della versione attiva.
func (c *Controller) keptVolumes(ctx context.Context, app store.App) (map[string]bool, error) {
	keep := map[string]bool{}
	current, err := c.st.ListAppVolumes(ctx, app.ID)
	if err != nil {
		return nil, err
	}
	for _, v := range current {
		keep[PVCName(v.Name)] = true
	}
	active, err := c.st.GetAppVersion(ctx, app.ID, app.ActiveVersion)
	if err != nil {
		return nil, err
	}
	for _, v := range active.Config.Volumes {
		keep[PVCName(v.Name)] = true
	}
	// Un deploy fallito lascia la versione attiva di prima: se l'ultimo non è «serving» non si toglie nulla.
	if app.LastDeploy != nil && app.LastDeploy.State != "serving" {
		return nil, fmt.Errorf("ultimo deploy %s", app.LastDeploy.State)
	}
	return keep, nil
}

// Forget toglie dal cluster il namespace dell'app, e con lui Deployment, rotta e PVC: i dati dei
// volumi vanno via insieme all'app (H5, v3-b). Idempotente: un namespace già assente non è un
// errore. Si chiama dopo l'eliminazione dell'app dal modello (la conferma sui volumi è già stata
// verificata dall'API) o allo scadere dei 7 giorni di un repo eliminato.
func (c *Controller) Forget(ctx context.Context, id uuid.UUID) error {
	// I PVC si eliminano per nome prima del namespace: i dati vanno via anche se il namespace
	// resta un po' in Terminating, e l'intento è esplicito (solo PVC con la label del controller).
	pvcs := c.kube.CoreV1().PersistentVolumeClaims(Namespace(id))
	if list, err := pvcs.List(ctx, metav1.ListOptions{LabelSelector: LabelVolume + "," + LabelManaged + "=true"}); err == nil {
		for _, p := range list.Items {
			if err := pvcs.Delete(ctx, p.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("volume %s dell'app %s: %w", p.Name, id, err)
			}
		}
	}
	err := c.kube.CoreV1().Namespaces().Delete(ctx, Namespace(id), metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("namespace dell'app %s: %w", id, err)
	}
	return nil
}

// PurgeRepoApps elimina dal cluster le app (e i volumi) di un repo che sta per essere cancellato
// definitivamente. Se il cluster non risponde ritorna l'errore: il repo resta nel cestino e il
// purge riprova alla corsa dopo, così i dati non restano orfani. Il modello cade in cascata con
// la riga del repo e lo spazio dei volumi torna libero nella quota dell'owner.
func (c *Controller) PurgeRepoApps(ctx context.Context, repoID uuid.UUID) error {
	list, err := c.st.ListApps(ctx, repoID)
	if err != nil {
		return fmt.Errorf("app del repo %s: %w", repoID, err)
	}
	for _, a := range list {
		if err := c.Forget(ctx, a.ID); err != nil {
			return err
		}
		c.cfg.Log.Info("apps: app e volumi eliminati con il repo", "app", a.ID, "repo", repoID)
	}
	return nil
}
