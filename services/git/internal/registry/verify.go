package registry

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"strings"
)

// Chi ha firmato un'immagine (H10, GIT-349).
const (
	// SignerNone: nessuna firma valida.
	SignerNone = ""
	// SignerInstance: la chiave dell'istanza, con cui la CI firma le immagini che pubblica con il
	// token del job; la firma nomina proprio quel repo e quell'immagine.
	SignerInstance = "instance"
	// SignerTrusted: una delle chiavi fidate aggiuntive dell'amministratore.
	SignerTrusted = "trusted"
)

// ErrInvalidTrustedKey: una chiave fidata non è una chiave pubblica ECDSA in PEM.
var ErrInvalidTrustedKey = errors.New("registry: chiave fidata non valida")

// ParsePublicKeyPEM legge una chiave pubblica cosign (ECDSA, PKIX in PEM).
func ParsePublicKeyPEM(b []byte) (*ecdsa.PublicKey, error) {
	blk, _ := pem.Decode(b)
	if blk == nil || blk.Type != "PUBLIC KEY" {
		return nil, ErrInvalidTrustedKey
	}
	k, err := x509.ParsePKIXPublicKey(blk.Bytes)
	if err != nil {
		return nil, ErrInvalidTrustedKey
	}
	pub, ok := k.(*ecdsa.PublicKey)
	if !ok {
		return nil, ErrInvalidTrustedKey
	}
	return pub, nil
}

// SignatureCheck è l'esito di VerifyImage.
type SignatureCheck struct {
	// Digest è il digest dell'immagine controllata.
	Digest string `json:"digest"`
	// Signer: SignerNone, SignerInstance o SignerTrusted.
	Signer string `json:"signer"`
	// KeyIndex: con SignerTrusted, la posizione della chiave nell'elenco passato.
	KeyIndex int `json:"keyIndex"`
}

// VerifyImage controlla la firma cosign del tag o del digest di un'immagine del repo (H10).
// La chiave dell'istanza vale solo se la firma nomina questo repo e questa immagine
// (repoRef = owner/nome del repo): una firma ricopiata da un altro repo non conta. Le chiavi
// fidate aggiuntive (PEM) valgono per qualunque nome. Con digest e tag vale il digest.
// «Nessuna firma» e «firma non valida» danno entrambe SignerNone.
func (h *Handler) VerifyImage(ctx context.Context, repoID, image, tag, digest, repoRef string, trusted [][]byte) (SignatureCheck, error) {
	if !uuidRe.MatchString(repoID) {
		return SignatureCheck{}, ErrPackageNotFound
	}
	name := repoID
	if image != "" {
		for _, seg := range strings.Split(image, "/") {
			if !imageSegRe.MatchString(seg) {
				return SignatureCheck{}, ErrInvalidImage
			}
		}
		name = repoID + "/" + image
	}
	pubs := make([]*ecdsa.PublicKey, 0, len(trusted))
	for _, t := range trusted {
		pub, err := ParsePublicKeyPEM(t)
		if err != nil {
			return SignatureCheck{}, err
		}
		pubs = append(pubs, pub)
	}
	all, err := h.tagsOf(ctx, name)
	if err != nil {
		return SignatureCheck{}, err
	}
	dg := ""
	switch {
	case digest != "":
		if _, ok := hexOf(digest); !ok {
			return SignatureCheck{}, ErrPackageNotFound
		}
		for t, d := range all {
			if _, aux := provenanceSubject(t); !aux && d == digest {
				dg = d
				break
			}
		}
	case tagRe.MatchString(tag):
		if _, aux := provenanceSubject(tag); !aux {
			dg = all[tag]
		}
	}
	if dg == "" {
		return SignatureCheck{}, ErrPackageNotFound
	}
	out := SignatureCheck{Digest: dg}
	sigDigest, ok := all[techTag(dg, ".sig")]
	if !ok {
		return out, nil
	}
	m, _, ok := h.readManifest(ctx, sigDigest)
	if !ok {
		return out, nil
	}
	want := strings.ToLower(repoRef)
	if image != "" {
		want += "/" + strings.ToLower(image)
	}
	for _, layer := range m.Layers {
		if h.signingKey != nil {
			if ref, ok := h.verifyCosignRef(ctx, &h.signingKey.PublicKey, layer, dg); ok && referencePath(ref) == want {
				out.Signer = SignerInstance
				return out, nil
			}
		}
		for i, pub := range pubs {
			if _, ok := h.verifyCosignRef(ctx, pub, layer, dg); ok {
				out.Signer, out.KeyIndex = SignerTrusted, i
				return out, nil
			}
		}
	}
	return out, nil
}

// referencePath toglie l'host da un riferimento «host/owner/repo[/immagine]» e lo mette in minuscolo.
func referencePath(ref string) string {
	_, p, ok := strings.Cut(ref, "/")
	if !ok {
		return ""
	}
	return strings.ToLower(p)
}
