package apps

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// GIT-460 (v3-c): validazione del certificato del cliente e Secret nel solo namespace di Traefik.

type testCert struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	der  []byte
}

func (c testCert) certPEM() string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.der}))
}

func (c testCert) keyPEM(t *testing.T) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(c.key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func mkCert(t *testing.T, cn string, dns []string, parent *testCert, ca bool, from, until time.Time) testCert {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: cn}, DNSNames: dns,
		NotBefore: from, NotAfter: until, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true, IsCA: ca,
	}
	if ca {
		tpl.KeyUsage |= x509.KeyUsageCertSign
	}
	signer, signKey := tpl, key
	if parent != nil {
		signer, signKey = parent.cert, parent.key
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, signer, &key.PublicKey, signKey)
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return testCert{cert: c, key: key, der: der}
}

type testPKI struct {
	root, inter, leaf testCert
	roots             *x509.CertPool
	now               time.Time
}

func newPKI(t *testing.T, leafDNS ...string) testPKI {
	t.Helper()
	now := time.Now()
	p := testPKI{now: now}
	p.root = mkCert(t, "Test Root", nil, nil, true, now.Add(-48*time.Hour), now.Add(48*time.Hour))
	p.inter = mkCert(t, "Test Intermediate", nil, &p.root, true, now.Add(-24*time.Hour), now.Add(24*time.Hour))
	p.leaf = mkCert(t, "leaf", leafDNS, &p.inter, false, now.Add(-time.Hour), now.Add(24*time.Hour))
	p.roots = x509.NewCertPool()
	p.roots.AddCert(p.root.cert)
	return p
}

func TestValidateCustomCertificate_Accettato(t *testing.T) {
	p := newPKI(t, "shop.retail.it")
	chain := p.leaf.certPEM() + p.inter.certPEM()
	info, err := ValidateCustomCertificate("shop.retail.it", chain, p.leaf.keyPEM(t), CertCheckOptions{Roots: p.roots, Now: p.now})
	if err != nil {
		t.Fatalf("catena completa: %v", err)
	}
	if info.Issuer != "Test Intermediate" || !info.ValidUntil.Equal(p.leaf.cert.NotAfter.UTC()) {
		t.Fatalf("info: %+v", info)
	}
	// La radice inclusa nella catena chiude la catena anche senza radici note.
	if _, err := ValidateCustomCertificate("shop.retail.it", chain+p.root.certPEM(), p.leaf.keyPEM(t), CertCheckOptions{Roots: x509.NewCertPool(), Now: p.now}); err != nil {
		t.Fatalf("radice in catena: %v", err)
	}
	// Wildcard di un livello.
	w := mkCert(t, "wild", []string{"*.retail.it"}, &p.inter, false, p.now.Add(-time.Hour), p.now.Add(time.Hour))
	if _, err := ValidateCustomCertificate("shop.retail.it", w.certPEM()+p.inter.certPEM(), w.keyPEM(t), CertCheckOptions{Roots: p.roots, Now: p.now}); err != nil {
		t.Fatalf("wildcard: %v", err)
	}
	if _, err := ValidateCustomCertificate("a.b.retail.it", w.certPEM()+p.inter.certPEM(), w.keyPEM(t), CertCheckOptions{Roots: p.roots, Now: p.now}); err == nil {
		t.Fatal("il wildcard copre un solo livello")
	}
}

func TestValidateCustomCertificate_Rifiuti(t *testing.T) {
	p := newPKI(t, "shop.retail.it")
	good := p.leaf.certPEM() + p.inter.certPEM()
	key := p.leaf.keyPEM(t)
	other := mkCert(t, "altra", []string{"shop.retail.it"}, &p.inter, false, p.now.Add(-time.Hour), p.now.Add(time.Hour))
	expired := mkCert(t, "old", []string{"shop.retail.it"}, &p.inter, false, p.now.Add(-48*time.Hour), p.now.Add(-time.Hour))
	selfSigned := mkCert(t, "self", []string{"shop.retail.it"}, nil, false, p.now.Add(-time.Hour), p.now.Add(time.Hour))
	opt := CertCheckOptions{Roots: p.roots, Now: p.now}
	for _, tc := range []struct {
		name, domain, chain, key, want string
	}{
		{"dominio non coperto", "altro.it", good, key, "non copre altro.it"},
		{"scaduto", "shop.retail.it", expired.certPEM() + p.inter.certPEM(), expired.keyPEM(t), "scaduto"},
		{"chiave diversa", "shop.retail.it", good, other.keyPEM(t), "non corrisponde"},
		{"catena incompleta", "shop.retail.it", p.leaf.certPEM(), key, "catena è incompleta"},
		{"autofirmato", "shop.retail.it", selfSigned.certPEM(), selfSigned.keyPEM(t), "autofirmato"},
		{"niente PEM", "shop.retail.it", "ciao", key, "nessun certificato PEM"},
		{"chiave nel campo certificato", "shop.retail.it", key, key, "privateKey"},
		{"chiave non PEM", "shop.retail.it", good, "ciao", "formato PEM"},
		{"chiave con passphrase", "shop.retail.it", good, "-----BEGIN ENCRYPTED PRIVATE KEY-----\nAAAA\n-----END ENCRYPTED PRIVATE KEY-----\n", "passphrase"},
	} {
		_, err := ValidateCustomCertificate(tc.domain, tc.chain, tc.key, opt)
		if !errors.Is(err, ErrCertificateInvalid) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v (atteso %q)", tc.name, err, tc.want)
			continue
		}
		// Il messaggio non riporta mai la chiave: né il PEM né i suoi byte.
		if strings.Contains(err.Error(), "PRIVATE KEY") || strings.Contains(err.Error(), keyBody(key)) {
			t.Errorf("%s: l'errore cita la chiave: %v", tc.name, err)
		}
	}
	// Scaduto vale anche per un intermedio.
	oldInter := mkCert(t, "OldInter", nil, &p.root, true, p.now.Add(-48*time.Hour), p.now.Add(-time.Hour))
	l := mkCert(t, "l", []string{"shop.retail.it"}, &oldInter, false, p.now.Add(-time.Hour), p.now.Add(time.Hour))
	if _, err := ValidateCustomCertificate("shop.retail.it", l.certPEM()+oldInter.certPEM(), l.keyPEM(t), opt); err == nil || !strings.Contains(err.Error(), "intermedio") {
		t.Errorf("intermedio scaduto: %v", err)
	}
}

func keyBody(pemKey string) string {
	return strings.Split(pemKey, "\n")[1]
}

// Il Secret del certificato sta solo nel namespace di Traefik e il TLSStore lo elenca; tolto il dominio
// custom il Secret sparisce; nessun Secret con la chiave nel namespace dell'app.
func TestSyncCustomSecrets_SoloNamespaceTraefik(t *testing.T) {
	kube := fake.NewClientset()
	dyn := fakeDynamic()
	appNS := Namespace(uuid.New())
	if _, err := kube.CoreV1().Namespaces().Create(context.Background(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: appNS}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	c := &Controller{kube: kube, dyn: dyn, cfg: Config{TraefikNamespace: "traefik-ns", Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: time.Now}}
	// Un TLSStore del chart già presente: defaultCertificate e un certificato non nostro restano.
	pre := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "traefik.io/v1alpha1", "kind": "TLSStore",
		"metadata": map[string]any{"name": "default", "namespace": "traefik-ns"},
		"spec": map[string]any{
			"defaultCertificate": map[string]any{"secretName": "gitstack-tls"},
			"certificates":       []any{map[string]any{"secretName": "altro"}},
		},
	}}
	if _, err := dyn.Resource(tlsStoreGVR).Namespace("traefik-ns").Create(context.Background(), pre, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	cert := store.CustomCertificate{DomainID: uuid.New(), Domain: "shop.retail.it", ChainPEM: "CATENA", KeyPEM: "CHIAVE-SEGRETA"}
	if err := c.syncCustomSecrets(context.Background(), []store.CustomCertificate{cert}); err != nil {
		t.Fatal(err)
	}
	name := CustomCertSecretName(cert.DomainID)
	sec, err := kube.CoreV1().Secrets("traefik-ns").Get(context.Background(), name, metav1.GetOptions{})
	if err != nil || sec.Type != corev1.SecretTypeTLS || string(sec.Data[corev1.TLSPrivateKeyKey]) != "CHIAVE-SEGRETA" {
		t.Fatalf("Secret nel namespace Traefik: %+v %v", sec, err)
	}
	all, _ := kube.CoreV1().Secrets(metav1.NamespaceAll).List(context.Background(), metav1.ListOptions{})
	for _, s := range all.Items {
		if s.Namespace != "traefik-ns" {
			t.Errorf("Secret fuori dal namespace Traefik: %s/%s", s.Namespace, s.Name)
		}
	}
	ts, _ := dyn.Resource(tlsStoreGVR).Namespace("traefik-ns").Get(context.Background(), "default", metav1.GetOptions{})
	spec := ts.Object["spec"].(map[string]any)
	refs := spec["certificates"].([]any)
	if len(refs) != 2 || refs[0].(map[string]any)["secretName"] != "altro" || refs[1].(map[string]any)["secretName"] != name {
		t.Fatalf("certificates del TLSStore: %v", refs)
	}
	if spec["defaultCertificate"].(map[string]any)["secretName"] != "gitstack-tls" {
		t.Fatal("il defaultCertificate non va toccato")
	}
	// Tornato a letsencrypt: Secret e riferimento spariscono, il resto resta.
	if err := c.syncCustomSecrets(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := kube.CoreV1().Secrets("traefik-ns").Get(context.Background(), name, metav1.GetOptions{}); err == nil {
		t.Fatal("il Secret del dominio non più custom va tolto")
	}
	ts, _ = dyn.Resource(tlsStoreGVR).Namespace("traefik-ns").Get(context.Background(), "default", metav1.GetOptions{})
	refs = ts.Object["spec"].(map[string]any)["certificates"].([]any)
	if len(refs) != 1 || refs[0].(map[string]any)["secretName"] != "altro" {
		t.Fatalf("certificates dopo la rimozione: %v", refs)
	}
	if strings.Contains(cert.String(), "CHIAVE-SEGRETA") {
		t.Fatal("String() non deve stampare la chiave")
	}
}

// Con la CA interna il TLSStore `default` è quello del chart, nel namespace del release, e core è
// configurato con quel namespace: dopo la sincronizzazione in tutto il cluster c'è UN SOLO TLSStore
// `default`, col defaultCertificate intatto e il nostro Secret in certificates; nessun Secret altrove.
func TestSyncCustomSecrets_UnSoloTLSStoreDefaultNelNamespaceDelChart(t *testing.T) {
	kube := fake.NewClientset()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{tlsStoreGVR: "TLSStoreList"})
	const releaseNS = "gitstack"
	c := &Controller{kube: kube, dyn: dyn, cfg: Config{TraefikNamespace: releaseNS, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: time.Now}}
	chartStore := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "traefik.io/v1alpha1", "kind": "TLSStore",
		"metadata": map[string]any{"name": "default", "namespace": releaseNS},
		"spec":     map[string]any{"defaultCertificate": map[string]any{"secretName": "gitstack-tls"}},
	}}
	if _, err := dyn.Resource(tlsStoreGVR).Namespace(releaseNS).Create(context.Background(), chartStore, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	cert := store.CustomCertificate{DomainID: uuid.New(), Domain: "shop.retail.it", ChainPEM: "CATENA", KeyPEM: "CHIAVE"}
	for i := 0; i < 2; i++ { // anche una seconda sincronizzazione non ne crea altri
		if err := c.syncCustomSecrets(context.Background(), []store.CustomCertificate{cert}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := dyn.Resource(tlsStoreGVR).Namespace(metav1.NamespaceAll).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.Items[0].GetName() != "default" || list.Items[0].GetNamespace() != releaseNS {
		t.Fatalf("serve un solo TLSStore default, nel namespace del chart: %d", len(list.Items))
	}
	spec := list.Items[0].Object["spec"].(map[string]any)
	if spec["defaultCertificate"].(map[string]any)["secretName"] != "gitstack-tls" {
		t.Fatal("il defaultCertificate dell'installer non va toccato")
	}
	refs := spec["certificates"].([]any)
	if len(refs) != 1 || refs[0].(map[string]any)["secretName"] != CustomCertSecretName(cert.DomainID) {
		t.Fatalf("certificates: %v", refs)
	}
	all, _ := kube.CoreV1().Secrets(metav1.NamespaceAll).List(context.Background(), metav1.ListOptions{})
	if len(all.Items) != 1 || all.Items[0].Namespace != releaseNS {
		t.Fatalf("il Secret deve stare solo nel namespace del TLSStore: %+v", all.Items)
	}
}
