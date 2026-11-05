package databases

import (
	"context"
	"fmt"
	"reflect"

	appsv1 "k8s.io/api/apps/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

func (c *Controller) ensureNetworkPolicy(ctx context.Context, ns string, obj any) error {
	want, ok := obj.(*networkingv1.NetworkPolicy)
	if !ok {
		return fmt.Errorf("tipo di oggetto non gestito: %T", obj)
	}
	policies := c.kube.NetworkingV1().NetworkPolicies(ns)
	cur, err := policies.Get(ctx, want.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = policies.Create(ctx, want, metav1.CreateOptions{})
		return ignoreExists(err)
	}
	if err != nil {
		return err
	}
	if reflect.DeepEqual(cur.Spec, want.Spec) {
		return nil
	}
	cur.Spec = want.Spec
	_, err = policies.Update(ctx, cur, metav1.UpdateOptions{})
	return err
}

// ensureStatefulSet crea lo StatefulSet o lo riallinea quando cambia il template (immagine, risorse): il
// ridimensionamento CPU/RAM riavvia il pod, la dimensione dichiarata non tocca il PVC (ADR punto 3).
func (c *Controller) ensureStatefulSet(ctx context.Context, d store.ManagedDB, img Image) (*appsv1.StatefulSet, error) {
	ns := Namespace(d.ID)
	want := BuildStatefulSet(d, StatefulSetOptions{Image: img, PriorityClass: c.cfg.PriorityClass, StorageClass: c.cfg.StorageClass})
	sets := c.kube.AppsV1().StatefulSets(ns)
	cur, err := sets.Get(ctx, WorkloadName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		created, err := sets.Create(ctx, want, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			return sets.Get(ctx, WorkloadName, metav1.GetOptions{})
		}
		return created, err
	}
	if err != nil {
		return nil, err
	}
	if cur.Labels[LabelDBID] != d.ID.String() {
		return nil, fatalf("lo StatefulSet del namespace %s non e' di questo database", ns)
	}
	if cur.Annotations[annotationSpecHash] == want.Annotations[annotationSpecHash] {
		return cur, nil
	}
	var out *appsv1.StatefulSet
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest, err := sets.Get(ctx, WorkloadName, metav1.GetOptions{})
		if err != nil {
			return err
		}
		latest.Spec.Template = want.Spec.Template
		if latest.Annotations == nil {
			latest.Annotations = map[string]string{}
		}
		latest.Annotations[annotationSpecHash] = want.Annotations[annotationSpecHash]
		out, err = sets.Update(ctx, latest, metav1.UpdateOptions{})
		return err
	})
	return out, err
}

// ensureRoles crea ruoli e database una sola volta (poi il Secret e' marcato): idempotente anche se si ripete.
func (c *Controller) ensureRoles(ctx context.Context, d store.ManagedDB) error {
	if c.sql == nil {
		return fatalf("nessun esecutore SQL configurato")
	}
	secrets := c.kube.CoreV1().Secrets(Namespace(d.ID))
	sec, err := secrets.Get(ctx, SecretAuth, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if sec.Annotations[annotationRoles] == "true" {
		return nil
	}
	pw := string(sec.Data["owner-password"])
	if pw == "" {
		return fatalf("il Secret %s non ha la password del proprietario", SecretAuth)
	}
	c.remember(pw)
	if err := SetupRoles(ctx, c.sql, d, pw); err != nil {
		return err
	}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cur, err := secrets.Get(ctx, SecretAuth, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if cur.Annotations == nil {
			cur.Annotations = map[string]string{}
		}
		cur.Annotations[annotationRoles] = "true"
		_, err = secrets.Update(ctx, cur, metav1.UpdateOptions{})
		return err
	})
}
