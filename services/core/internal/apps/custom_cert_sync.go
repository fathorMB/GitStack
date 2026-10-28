package apps

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/retry"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Certificati del cliente nel cluster (v3-c, GIT-460, ADR 0002). Il Secret TLS di un certificato del
// cliente sta SOLO nel namespace di Traefik, e Traefik lo serve per SNI perché è elencato nel TLSStore
// `default` di quel namespace: nel namespace dell'app non entra mai né il Secret né la chiave. Le
// IngressRoute del dominio dichiarano `tls: {}` come quelle dell'indirizzo automatico.

const (
	// LabelCustomCert marca i Secret creati da core per i certificati del cliente: è l'unico perimetro in cui
	// core toglie Secret dal namespace di Traefik.
	LabelCustomCert = "gitstack.io/custom-domain-cert"
	// LabelCustomCertDomain: id del dominio a cui appartiene il Secret.
	LabelCustomCertDomain = "gitstack.io/custom-domain-id"
	secretNamePrefix      = "gitstack-domain-"
	defaultTraefikNS      = "kube-system"
	tlsStoreName          = "default"
)

var tlsStoreGVR = schema.GroupVersionResource{Group: "traefik.io", Version: "v1alpha1", Resource: "tlsstores"}

// CustomCertSecretName è il nome del Secret TLS del dominio nel namespace di Traefik.
func CustomCertSecretName(domainID uuid.UUID) string { return secretNamePrefix + domainID.String() }

func (c *Controller) traefikNamespace() string {
	if ns := strings.TrimSpace(c.cfg.TraefikNamespace); ns != "" {
		return ns
	}
	return defaultTraefikNS
}

// BuildCustomCertSecret è il Secret TLS del certificato del cliente, per il namespace di Traefik.
func BuildCustomCertSecret(namespace string, cert store.CustomCertificate) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: CustomCertSecretName(cert.DomainID), Namespace: namespace,
			Labels: map[string]string{LabelCustomCert: "true", LabelCustomCertDomain: cert.DomainID.String()},
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{corev1.TLSCertKey: []byte(cert.ChainPEM), corev1.TLSPrivateKeyKey: []byte(cert.KeyPEM)},
	}
}

// SyncCustomCerts riallinea il cluster ai certificati del cliente salvati: un Secret per dominio nel
// namespace di Traefik, il TLSStore `default` che li elenca, e via i Secret dei domini che non sono più
// custom. Segna in errore un certificato scaduto. Senza Keyring (chiave dei segreti) non fa niente.
func (c *Controller) SyncCustomCerts(ctx context.Context) {
	if c.st == nil || c.kube == nil || c.cfg.Secrets == nil {
		return
	}
	certs, err := c.st.CustomCertificates(ctx, c.cfg.Secrets)
	if err != nil {
		if ctx.Err() == nil {
			c.cfg.Log.Warn("apps: lettura dei certificati del cliente non riuscita", "err", err)
		}
		return
	}
	now := c.cfg.Now()
	for _, cert := range certs {
		if cert.ValidUntil != nil && now.After(*cert.ValidUntil) && cert.State != "error" {
			msg := "il certificato caricato è scaduto: caricane uno nuovo o torna a Let's Encrypt"
			if _, err := c.st.SetCustomCertState(ctx, cert.DomainID, "error", &msg, now); err != nil && ctx.Err() == nil {
				c.cfg.Log.Warn("apps: stato del certificato del cliente non scritto", "domain", cert.Domain, "err", err)
			}
		}
	}
	if err := c.syncCustomSecrets(ctx, certs); err != nil && ctx.Err() == nil {
		// L'errore del cluster non cita mai il contenuto dei Secret.
		c.cfg.Log.Warn("apps: certificati del cliente non riallineati", "err", err)
	}
}

func (c *Controller) syncCustomSecrets(ctx context.Context, certs []store.CustomCertificate) error {
	ns := c.traefikNamespace()
	secrets := c.kube.CoreV1().Secrets(ns)
	want := map[string]bool{}
	for _, cert := range certs {
		sec := BuildCustomCertSecret(ns, cert)
		want[sec.Name] = true
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			cur, err := secrets.Get(ctx, sec.Name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				_, err = secrets.Create(ctx, sec.DeepCopy(), metav1.CreateOptions{})
				return err
			}
			if err != nil {
				return err
			}
			if string(cur.Data[corev1.TLSCertKey]) == string(sec.Data[corev1.TLSCertKey]) &&
				string(cur.Data[corev1.TLSPrivateKeyKey]) == string(sec.Data[corev1.TLSPrivateKeyKey]) && cur.Type == sec.Type {
				return nil
			}
			cur.Data, cur.Labels = sec.Data, sec.Labels
			_, err = secrets.Update(ctx, cur, metav1.UpdateOptions{})
			return err
		})
		if err != nil {
			return fmt.Errorf("secret del certificato di %s: %w", cert.Domain, err)
		}
	}
	// Prima il TLSStore (senza i Secret da togliere), poi i Secret: Traefik non cita mai un Secret assente.
	names := make([]string, 0, len(want))
	for n := range want {
		names = append(names, n)
	}
	sort.Strings(names)
	previous, err := c.syncTLSStore(ctx, ns, names)
	if err != nil {
		return fmt.Errorf("tlsstore %s: %w", tlsStoreName, err)
	}
	// I Secret da togliere sono quelli che il TLSStore citava e ora non servono più: core non ha il
	// permesso di elencare i Secret del namespace di Traefik (RBAC non filtra una list per nome).
	for _, n := range previous {
		if want[n] {
			continue
		}
		if err := secrets.Delete(ctx, n, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// syncTLSStore scrive in spec.certificates del TLSStore `default` i Secret dei certificati del cliente,
// lasciando intatto il resto (defaultCertificate e i certificati che non sono nostri). Se il TLSStore non
// c'è lo crea con i soli certificati.
func (c *Controller) syncTLSStore(ctx context.Context, ns string, ours []string) (previous []string, err error) {
	if c.dyn == nil {
		return nil, nil
	}
	ri := c.dyn.Resource(tlsStoreGVR).Namespace(ns)
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cur, err := ri.Get(ctx, tlsStoreName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			if len(ours) == 0 {
				return nil
			}
			obj := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "traefik.io/v1alpha1", "kind": "TLSStore",
				"metadata": map[string]any{"name": tlsStoreName, "namespace": ns},
				"spec":     map[string]any{"certificates": certRefs(ours, nil)},
			}}
			_, err = ri.Create(ctx, obj, metav1.CreateOptions{})
			return err
		}
		if err != nil {
			return err
		}
		spec, _ := cur.Object["spec"].(map[string]any)
		if spec == nil {
			spec = map[string]any{}
		}
		existing, _ := spec["certificates"].([]any)
		previous = ourRefs(existing)
		refs := certRefs(ours, existing)
		if sameRefs(existing, refs) {
			return nil
		}
		if len(refs) == 0 {
			delete(spec, "certificates")
		} else {
			spec["certificates"] = refs
		}
		cur.Object["spec"] = spec
		_, err = ri.Update(ctx, cur, metav1.UpdateOptions{})
		return err
	})
	return previous, err
}

// ourRefs sono i Secret dei certificati del cliente citati dal TLSStore.
func ourRefs(existing []any) []string {
	var out []string
	for _, e := range existing {
		m, _ := e.(map[string]any)
		if n, _ := m["secretName"].(string); strings.HasPrefix(n, secretNamePrefix) {
			out = append(out, n)
		}
	}
	return out
}

// certRefs è la lista dei certificati del TLSStore: quelli non nostri come stavano, poi i nostri.
func certRefs(ours []string, existing []any) []any {
	out := []any{}
	for _, e := range existing {
		m, _ := e.(map[string]any)
		if n, _ := m["secretName"].(string); !strings.HasPrefix(n, secretNamePrefix) {
			out = append(out, e)
		}
	}
	for _, n := range ours {
		out = append(out, map[string]any{"secretName": n})
	}
	return out
}

func sameRefs(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		am, _ := a[i].(map[string]any)
		bm, _ := b[i].(map[string]any)
		if am["secretName"] != bm["secretName"] {
			return false
		}
	}
	return true
}
