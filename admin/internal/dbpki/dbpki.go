// Package dbpki crea i segreti di lunga vita dei database gestiti (ADR 0003, GIT-565): la CA
// intermedia dei database e la chiave di dump dell'istanza.
//
// Li crea l'installer (`gitstack databases ensure`, chiamato da install.sh) e `gitstack upgrade` su
// un'installazione esistente, mai il chart: la chiave della radice resta sull'host (ADR 0002) e core
// riceve solo l'intermedia. Entrambi sono Secret con `helm.sh/resource-policy: keep` e label
// part-of=gitstack, quindi finiscono in secrets.json di `gitstack backup` e tornano con `gitstack restore`
// (admin/internal/backup/cluster.go, restore.go) senza codice dedicato.
//
// Ensure è idempotente: un Secret che esiste non si tocca mai (la chiave di dump cifra i dump già
// scritti, e i certificati dei database firmati dall'intermedia restano validi).
package dbpki

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/admin/internal/backup"
)

const (
	// ClusterDomain è l'unico dominio del cluster che l'intermedia può firmare (ADR 0003, «Certificati»).
	ClusterDomain = "svc.cluster.local"
	// DumpKeyLen: AES-256-GCM, come l'archivio di backup (crypto.go).
	DumpKeyLen = 32
	// Validità dell'intermedia, al massimo quella della radice.
	intermediateValidity = 5 * 365 * 24 * time.Hour
	// Chiavi dei Secret (stesso contratto con core, GITSTACK_CORE_DATABASES_CA_DIR e _DUMP_KEY).
	CACertKey   = "tls.crt"
	CAKeyKey    = "tls.key"
	DumpKeyData = "key"
)

// ErrNoRootCA: la radice dell'istanza non è sull'host (certificato del cliente, senza CA interna).
var ErrNoRootCA = errors.New("la CA radice dell'istanza (ca.crt e ca.key) non è nella cartella TLS")

func trim(name string) string {
	if len(name) > 63 {
		name = name[:63]
	}
	return strings.TrimRight(name, "-")
}

// CASecretName è il Secret con l'intermedia (stesso nome di gitstack.databases.caSecret nel chart).
func CASecretName(release string) string { return trim(release + "-db-ca") }

// DumpKeySecretName è il Secret con la chiave di dump (gitstack.databases.dumpKeySecret).
func DumpKeySecretName(release string) string { return trim(release + "-db-dump-key") }

// NewIntermediate firma una CA intermedia con le name constraints dell'ADR 0003: solo
// *.svc.cluster.local e i nomi e gli IP dell'istanza (più il loopback, che gitstack-tls mette sempre).
// Ritorna certificato e chiave PEM (la chiave in PKCS#8).
func NewIntermediate(rootCertPEM, rootKeyPEM []byte, names, ips []string, now time.Time) (certPEM, keyPEM []byte, err error) {
	root, err := parseCert(rootCertPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("certificato della radice: %w", err)
	}
	rootKey, err := parseKey(rootKeyPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("chiave della radice: %w", err)
	}
	if !root.IsCA {
		return nil, nil, errors.New("il certificato della radice non è una CA")
	}
	permittedDNS := []string{ClusterDomain}
	seen := map[string]bool{ClusterDomain: true}
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "" || seen[n] {
			continue
		}
		if strings.ContainsAny(n, " /*@") {
			return nil, nil, fmt.Errorf("nome non ammesso nelle name constraints: %q", n)
		}
		seen[n] = true
		permittedDNS = append(permittedDNS, n)
	}
	var permittedIPs []*net.IPNet
	seenIP := map[string]bool{}
	for _, s := range append(append([]string{}, ips...), "127.0.0.1") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		ip := net.ParseIP(s)
		if ip == nil {
			return nil, nil, fmt.Errorf("indirizzo IP non valido nelle name constraints: %q", s)
		}
		bits := 128
		if v4 := ip.To4(); v4 != nil {
			ip, bits = v4, 32
		}
		if seenIP[ip.String()] {
			continue
		}
		seenIP[ip.String()] = true
		permittedIPs = append(permittedIPs, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, nil, err
	}
	notAfter := now.Add(intermediateValidity)
	if root.NotAfter.Before(notAfter) {
		notAfter = root.NotAfter
	}
	tmpl := &x509.Certificate{
		SerialNumber:                serial,
		Subject:                     pkix.Name{CommonName: "GitStack Database CA", Organization: []string{"GitStack"}},
		NotBefore:                   now.Add(-time.Hour),
		NotAfter:                    notAfter,
		IsCA:                        true,
		BasicConstraintsValid:       true,
		MaxPathLen:                  0,
		MaxPathLenZero:              true,
		KeyUsage:                    x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         permittedDNS,
		PermittedIPRanges:           permittedIPs,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, root, &key.PublicKey, rootKey)
	if err != nil {
		return nil, nil, fmt.Errorf("firma dell'intermedia: %w", err)
	}
	kd, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd})
	return certPEM, keyPEM, nil
}

func parseCert(b []byte) (*x509.Certificate, error) {
	blk, _ := pem.Decode(b)
	if blk == nil || blk.Type != "CERTIFICATE" {
		return nil, errors.New("PEM non valido")
	}
	return x509.ParseCertificate(blk.Bytes)
}

func parseKey(b []byte) (any, error) {
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, errors.New("PEM non valido")
	}
	switch blk.Type {
	case "EC PRIVATE KEY":
		return x509.ParseECPrivateKey(blk.Bytes)
	default:
		return x509.ParsePKCS8PrivateKey(blk.Bytes)
	}
}

// Instance sono i nomi e gli IP dell'istanza (TLS_NAMES e TLS_IPS di tls.conf).
type Instance struct {
	Names []string
	IPs   []string
}

// ReadInstance legge nomi e IP da tls.conf nella cartella TLS. Un file assente dà un'istanza vuota.
func ReadInstance(tlsDir string) (Instance, error) {
	b, err := os.ReadFile(filepath.Join(tlsDir, "tls.conf"))
	if errors.Is(err, os.ErrNotExist) {
		return Instance{}, nil
	}
	if err != nil {
		return Instance{}, err
	}
	var in Instance
	for _, l := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		l = strings.TrimSpace(l)
		for _, kv := range []struct {
			key string
			dst *[]string
		}{{"TLS_NAMES=", &in.Names}, {"TLS_IPS=", &in.IPs}} {
			if v, ok := strings.CutPrefix(l, kv.key); ok {
				v = strings.Trim(v, `"'`)
				for _, p := range strings.Split(v, ",") {
					if p = strings.TrimSpace(p); p != "" {
						*kv.dst = append(*kv.dst, p)
					}
				}
			}
		}
	}
	return in, nil
}

// Result dice cosa ha creato Ensure.
type Result struct {
	CreatedCA      bool
	CreatedDumpKey bool
}

// Options per Ensure.
type Options struct {
	Cluster backup.Cluster
	Release string
	// TLSDir contiene ca.crt, ca.key e tls.conf dell'installer (gitstack-tls).
	TLSDir string
	// Now e Rand sono iniettabili nei test.
	Now  func() time.Time
	Rand func(b []byte) error
}

func (o *Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func (o *Options) randBytes(b []byte) error {
	if o.Rand != nil {
		return o.Rand(b)
	}
	_, err := rand.Read(b)
	return err
}

// Ensure crea la chiave di dump e l'intermedia dei database se mancano e non tocca quelle che ci sono.
// La chiave di dump si crea per prima: serve anche dove la radice non c'è. Se la radice non è sull'host
// ritorna ErrNoRootCA (con la chiave di dump già creata): chi chiama decide se è fatale.
func Ensure(ctx context.Context, o Options) (Result, error) {
	var res Result
	existing, err := o.Cluster.Secrets(ctx)
	if err != nil {
		return res, fmt.Errorf("lettura dei Secret: %w", err)
	}
	has := map[string]bool{}
	for _, s := range existing {
		has[s.Name] = true
	}

	if name := DumpKeySecretName(o.Release); !has[name] {
		key := make([]byte, DumpKeyLen)
		if err := o.randBytes(key); err != nil {
			return res, fmt.Errorf("chiave di dump: %w", err)
		}
		s, err := secret(name, o.Release, map[string][]byte{DumpKeyData: key})
		if err != nil {
			return res, err
		}
		if err := o.Cluster.ApplySecret(ctx, s); err != nil {
			return res, fmt.Errorf("creazione del Secret %s: %w", name, err)
		}
		res.CreatedDumpKey = true
	}

	if name := CASecretName(o.Release); !has[name] {
		crt, err := os.ReadFile(filepath.Join(o.TLSDir, "ca.crt"))
		if err != nil {
			return res, fmt.Errorf("%w: %v", ErrNoRootCA, err)
		}
		rk, err := os.ReadFile(filepath.Join(o.TLSDir, "ca.key"))
		if err != nil {
			return res, fmt.Errorf("%w: %v", ErrNoRootCA, err)
		}
		in, err := ReadInstance(o.TLSDir)
		if err != nil {
			return res, fmt.Errorf("tls.conf: %w", err)
		}
		cert, key, err := NewIntermediate(crt, rk, in.Names, in.IPs, o.now())
		if err != nil {
			return res, fmt.Errorf("CA intermedia dei database: %w", err)
		}
		s, err := secret(name, o.Release, map[string][]byte{CACertKey: cert, CAKeyKey: key})
		if err != nil {
			return res, err
		}
		if err := o.Cluster.ApplySecret(ctx, s); err != nil {
			return res, fmt.Errorf("creazione del Secret %s: %w", name, err)
		}
		res.CreatedCA = true
	}
	return res, nil
}

// secret costruisce il Secret come lo restituisce KubectlCluster.Secrets: label part-of=gitstack (così
// entra in secrets.json) e resource-policy keep (sopravvive a helm uninstall).
func secret(name, release string, data map[string][]byte) (backup.Secret, error) {
	enc := map[string]string{}
	for k, v := range data {
		enc[k] = base64.StdEncoding.EncodeToString(v)
	}
	obj := map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name": name,
			"labels": map[string]string{
				"app.kubernetes.io/part-of":    "gitstack",
				"app.kubernetes.io/name":       "core",
				"app.kubernetes.io/instance":   release,
				"app.kubernetes.io/component":  "databases",
				"app.kubernetes.io/managed-by": "gitstack-admin",
			},
			"annotations": map[string]string{"helm.sh/resource-policy": "keep"},
		},
		"type": "Opaque",
		"data": enc,
	}
	raw, err := json.Marshal(obj)
	if err != nil {
		return backup.Secret{}, err
	}
	return backup.Secret{Name: name, Raw: raw}, nil
}
