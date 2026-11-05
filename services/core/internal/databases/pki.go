package databases

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// Certificato del server (ADR 0003 punto 6, GIT-565): core non ha la chiave della radice, ha solo la CA
// INTERMEDIA dei database (Secret db-ca montato in CADir: tls.crt e tls.key, PKCS#8). La foglia porta
// come SAN il nome interno del Service, che l'intermedia ha tra le name constraints (*.svc.cluster.local).
// I client si fidano della sola radice gia' distribuita: il certificato del server porta foglia + intermedia.

const (
	leafValidity = 365 * 24 * time.Hour
	// RenewBefore: il rinnovo (GIT-554) parte a 30 giorni dalla scadenza.
	RenewBefore = 30 * 24 * time.Hour
)

// ErrNoDatabaseCA: la CA intermedia non e' montata (l'installer non l'ha creata).
var ErrNoDatabaseCA = errors.New("CA intermedia dei database non trovata: va creata dall'installer (gitstack databases ensure)")

// ServerCert e' la catena foglia+intermedia e la chiave della foglia, in PEM.
type ServerCert struct {
	ChainPEM []byte
	KeyPEM   []byte
	NotAfter time.Time
}

// IssueServerCert firma la foglia con l'intermedia in caDir.
func IssueServerCert(caDir string, dnsNames []string, now time.Time) (ServerCert, error) {
	certPEM, err := os.ReadFile(filepath.Join(caDir, "tls.crt"))
	if err != nil {
		return ServerCert{}, fmt.Errorf("%w: %v", ErrNoDatabaseCA, err)
	}
	keyPEM, err := os.ReadFile(filepath.Join(caDir, "tls.key"))
	if err != nil {
		return ServerCert{}, fmt.Errorf("%w: %v", ErrNoDatabaseCA, err)
	}
	cb, _ := pem.Decode(certPEM)
	kb, _ := pem.Decode(keyPEM)
	if cb == nil || kb == nil {
		return ServerCert{}, errors.New("CA intermedia dei database: PEM non valido")
	}
	ca, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return ServerCert{}, fmt.Errorf("certificato dell'intermedia: %w", err)
	}
	caKeyAny, err := x509.ParsePKCS8PrivateKey(kb.Bytes)
	if err != nil {
		return ServerCert{}, fmt.Errorf("chiave dell'intermedia: %w", err)
	}
	caKey, ok := caKeyAny.(*ecdsa.PrivateKey)
	if !ok {
		return ServerCert{}, errors.New("la chiave dell'intermedia non e' ECDSA")
	}
	if !ca.IsCA {
		return ServerCert{}, errors.New("il certificato dell'intermedia non e' una CA")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return ServerCert{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return ServerCert{}, err
	}
	notAfter := now.Add(leafValidity)
	if ca.NotAfter.Before(notAfter) {
		notAfter = ca.NotAfter.Add(-time.Minute)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: dnsNames[0], Organization: []string{"GitStack"}},
		DNSNames:     dnsNames,
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return ServerCert{}, fmt.Errorf("firma del certificato del server: %w", err)
	}
	kd, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return ServerCert{}, err
	}
	chain := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), certPEM...)
	return ServerCert{ChainPEM: chain, KeyPEM: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd}), NotAfter: notAfter}, nil
}
