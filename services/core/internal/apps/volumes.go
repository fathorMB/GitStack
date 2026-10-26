package apps

import (
	"context"
	"fmt"

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
