package registry

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// simpleSigning è il payload legacy che cosign verifica per una firma OCI.
type simpleSigning struct {
	Critical struct {
		Identity struct {
			DockerReference string `json:"docker-reference"`
		} `json:"identity"`
		Image struct {
			DockerManifestDigest string `json:"docker-manifest-digest"`
		} `json:"image"`
		Type string `json:"type"`
	} `json:"critical"`
}

func (h *Handler) signPayload(reference, digest string) ([]byte, string, error) {
	if h.signingKey == nil {
		return nil, "", errors.New("firma del registry non configurata")
	}
	var p simpleSigning
	p.Critical.Identity.DockerReference = reference
	p.Critical.Image.DockerManifestDigest = digest
	p.Critical.Type = "cosign container image signature"
	b, err := json.Marshal(p)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(b)
	sig, err := ecdsa.SignASN1(rand.Reader, h.signingKey, sum[:])
	if err != nil {
		return nil, "", err
	}
	return b, base64.StdEncoding.EncodeToString(sig), nil
}

// PublicKeyPEM restituisce la chiave pubblica che i client usano con
// `cosign verify --key`. Non espone mai la chiave privata.
func (h *Handler) PublicKeyPEM() ([]byte, error) {
	if h.signingKey == nil {
		return nil, errors.New("firma del registry non configurata")
	}
	return publicKeyPEM(h.signingKey)
}

// ServeSigningKey espone esclusivamente la chiave pubblica agli utenti
// autenticati; il gateway conserva l'header Bearer quando inoltra questa rotta.
func (h *Handler) ServeSigningKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	authorization := r.Header.Get("Authorization")
	token := strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
	if token == "" || token == authorization {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	_, valid, err := h.backend.VerifyToken(r.Context(), token)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if !valid {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	pem, err := h.PublicKeyPEM()
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	_, _ = w.Write(pem)
}

// loadSigningKey legge la chiave dell'istanza. Fuori da Kubernetes la crea
// alla prima esecuzione, con permessi leggibili solo dal processo del servizio.
func loadSigningKey(file string) (*ecdsa.PrivateKey, error) {
	if file == "" {
		return nil, errors.New("file della chiave di firma mancante")
	}
	b, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		key, genErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if genErr != nil {
			return nil, genErr
		}
		der, marshalErr := x509.MarshalPKCS8PrivateKey(key)
		if marshalErr != nil {
			return nil, marshalErr
		}
		if mkdirErr := os.MkdirAll(filepath.Dir(file), 0o700); mkdirErr != nil {
			return nil, mkdirErr
		}
		if writeErr := os.WriteFile(file, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); writeErr != nil {
			return nil, writeErr
		}
		return key, nil
	}
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, errors.New("chiave di firma non in PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("chiave di firma: %w", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, errors.New("la chiave di firma deve essere ECDSA P-256")
	}
	return key, nil
}

func publicKeyPEM(key *ecdsa.PrivateKey) ([]byte, error) {
	b, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: b}), nil
}

// spdxSBOM crea un documento SPDX minimale ma valido per rendere tracciabile
// l'immagine pubblicata dal job anche quando il job non ha prodotto uno SBOM.
func spdxSBOM(subject string) ([]byte, error) {
	doc := struct {
		SPDXVersion       string `json:"spdxVersion"`
		DataLicense       string `json:"dataLicense"`
		SPDXID            string `json:"SPDXID"`
		Name              string `json:"name"`
		DocumentNamespace string `json:"documentNamespace"`
		CreationInfo      struct {
			Created  string   `json:"created"`
			Creators []string `json:"creators"`
		} `json:"creationInfo"`
		Packages []any `json:"packages"`
	}{
		SPDXVersion: "SPDX-2.3", DataLicense: "CC0-1.0", SPDXID: "SPDXRef-DOCUMENT",
		Name: "GitStack registry image", DocumentNamespace: "urn:gitstack:registry:" + subject,
		Packages: []any{},
	}
	doc.CreationInfo.Created = time.Now().UTC().Format(time.RFC3339)
	doc.CreationInfo.Creators = []string{"Tool: GitStack registry"}
	return json.Marshal(doc)
}
