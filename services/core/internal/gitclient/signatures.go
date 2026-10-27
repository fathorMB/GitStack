package gitclient

import (
	"context"
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
)

// Firme delle immagini (H10, GIT-349): il servizio git legge il registry e verifica le firme
// cosign; core decide cosa farne.

// Chi ha firmato un'immagine, come lo dice il servizio git.
const (
	SignerNone     = ""
	SignerInstance = "instance"
	SignerTrusted  = "trusted"
)

// SignatureCheck è l'esito di VerifyImage.
type SignatureCheck struct {
	Digest string `json:"digest"`
	Signer string `json:"signer"`
	// KeyIndex: con SignerTrusted, la posizione nell'elenco di chiavi passato.
	KeyIndex int `json:"keyIndex"`
}

// ImageVerifier verifica la firma di un'immagine del registry del repo.
type ImageVerifier interface {
	// VerifyImage controlla il tag o il digest (con entrambi vale il digest) dell'immagine image
	// (vuoto = senza nome) del repo repoID; repoRef è owner/nome, a cui la firma della CI deve
	// riferirsi. trustedKeys sono chiavi pubbliche PEM fidate in aggiunta a quella dell'istanza.
	// ErrNotFound se l'immagine o il tag non ci sono.
	VerifyImage(ctx context.Context, caller trust.Identity, repoID uuid.UUID, image, tag, digest, repoRef string, trustedKeys []string) (SignatureCheck, error)
	// InstanceSigningKey dà la chiave pubblica (PEM) con cui la CI firma le immagini.
	InstanceSigningKey(ctx context.Context, caller trust.Identity) (string, error)
}

// VerifyImage implementa ImageVerifier: POST .../packages/verify.
func (c *Client) VerifyImage(ctx context.Context, caller trust.Identity, repoID uuid.UUID, image, tag, digest, repoRef string, trustedKeys []string) (SignatureCheck, error) {
	in := map[string]any{"image": image, "tag": tag, "digest": digest, "repo": repoRef, "trustedKeys": trustedKeys}
	var out SignatureCheck
	if err := c.do(ctx, caller, http.MethodPost, "/internal/git/repos/"+repoID.String()+"/packages/verify", in, &out, http.StatusOK); err != nil {
		return SignatureCheck{}, err
	}
	return out, nil
}

// InstanceSigningKey implementa ImageVerifier: GET /internal/git/registry/signing-key.
func (c *Client) InstanceSigningKey(ctx context.Context, caller trust.Identity) (string, error) {
	var out struct {
		PublicKey string `json:"publicKey"`
	}
	if err := c.do(ctx, caller, http.MethodGet, "/internal/git/registry/signing-key", nil, &out, http.StatusOK); err != nil {
		return "", err
	}
	return out.PublicKey, nil
}

var _ ImageVerifier = (*Client)(nil)
