package registry

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/opencontainers/go-digest"
)

// Elenco ed eliminazione dei pacchetti (GIT-361, RG1). Li chiama core, che ha già deciso i
// permessi dell'utente: qui si legge il registry (nomi, tag, digest, dimensioni, firme, SBOM)
// e si elimina passando da distribution, mai dal disco.

var (
	// ErrPackageNotFound: l'immagine o il tag non esistono.
	ErrPackageNotFound = errors.New("registry: pacchetto non trovato")
	// ErrProtectedTag: un tag protetto (RG2) non si elimina.
	ErrProtectedTag = errors.New("registry: tag protetto")
	// ErrInvalidImage: il nome dell'immagine non è valido.
	ErrInvalidImage = errors.New("registry: nome dell'immagine non valido")
)

// Chi ha pubblicato.
const (
	PusherUser  = "user"
	PusherAgent = "agent"
	PusherJob   = "job"
	// JobLogin è il login storico dei push fatti con un token di job.
	JobLogin = "ci job token"
)

// Pusher è chi ha pubblicato un tag, come autenticato dal registry al momento del push.
type Pusher struct {
	Kind  string `json:"kind"`
	Login string `json:"login"`
}

// Valori di firma.
const (
	SignatureVerified   = "verified"
	SignatureUnverified = "unverified"
)

// PackageTag è un tag di un'immagine. Revision è il commit dell'etichetta OCI
// org.opencontainers.image.revision (H14, GIT-386): assente se l'immagine non ne ha una valida.
type PackageTag struct {
	Name      string    `json:"name"`
	Digest    string    `json:"digest"`
	SizeBytes int64     `json:"sizeBytes"`
	PushedAt  time.Time `json:"pushedAt"`
	Signature string    `json:"signature"`
	HasSbom   bool      `json:"hasSbom"`
	Protected bool      `json:"protected"`
	PushedBy  *Pusher   `json:"pushedBy,omitempty"`
	Revision  string    `json:"revision,omitempty"`
}

// RevisionLabel è l'etichetta OCI con il commit da cui è nata l'immagine (H14).
const RevisionLabel = "org.opencontainers.image.revision"

// revisionRe accetta un commit: esadecimale da 7 a 64 caratteri. Un'etichetta che non lo è
// (un nome di branch, una versione) non diventa GITSTACK_COMMIT_SHA.
var revisionRe = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// Package è un'immagine del repo. Image è il nome sotto il repo (vuoto = l'immagine senza
// nome). Digest, firma, SBOM, dimensione e data sono quelli del tag più recente.
type Package struct {
	Image       string       `json:"image"`
	Tags        []string     `json:"tags"`
	TagDetails  []PackageTag `json:"tagDetails"`
	Digest      string       `json:"digest"`
	SizeBytes   int64        `json:"sizeBytes"`
	UpdatedAt   time.Time    `json:"updatedAt"`
	Signature   string       `json:"signature"`
	HasSbom     bool         `json:"hasSbom"`
	Kind        string       `json:"kind"`
	PushedBy    *Pusher      `json:"pushedBy,omitempty"`
	RemovedTags []RemovedTag `json:"removedTags,omitempty"`
}

// pusherPath è il file dove si ricorda chi ha pubblicato un tag. Sta sotto «_gitstack», che né
// distribution né imageNames scambiano per un'immagine, e sparisce con il repo (Purge).
func pusherPath(name, tag string) string {
	return repoRoot + name + "/_gitstack/pushers/" + tag + ".json"
}

// recordPusher ricorda chi ha pubblicato il tag. Un errore non fa fallire il push: il campo
// resterà assente.
func (h *Handler) recordPusher(ctx context.Context, name, tag string, p Pusher) {
	if tag == "" || strings.Contains(tag, ":") || !tagRe.MatchString(tag) {
		return
	}
	b, err := json.Marshal(p)
	if err == nil {
		err = h.driver.PutContent(ctx, pusherPath(name, tag), b)
	}
	if err != nil {
		h.log.Warn("autore del push non registrato", "err", err, "image", name, "tag", tag)
	}
}

func (h *Handler) readPusher(ctx context.Context, name, tag string) *Pusher {
	b, err := h.driver.GetContent(ctx, pusherPath(name, tag))
	if err != nil {
		return nil
	}
	var p Pusher
	if json.Unmarshal(b, &p) != nil || p.Login == "" {
		return nil
	}
	switch p.Kind {
	case PusherUser, PusherAgent, PusherJob:
		return &p
	}
	return nil
}

var (
	tagRe      = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	imageSegRe = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*$`)
)

type descriptor struct {
	MediaType    string `json:"mediaType"`
	ArtifactType string `json:"artifactType"`
	Digest       string `json:"digest"`
	Size         int64  `json:"size"`
	// Annotations serve alla firma cosign: la firma sta nell'annotation del layer.
	Annotations map[string]string `json:"annotations"`
}

type manifestDoc struct {
	MediaType    string       `json:"mediaType"`
	ArtifactType string       `json:"artifactType"`
	Config       *descriptor  `json:"config"`
	Layers       []descriptor `json:"layers"`
	Manifests    []descriptor `json:"manifests"`
}

func hexOf(dg string) (string, bool) {
	d := digest.Digest(dg)
	if d.Validate() != nil || d.Algorithm() != digest.SHA256 {
		return "", false
	}
	return d.Encoded(), true
}

// readManifest legge il manifest dal suo blob; ok false se manca o non è un manifest.
func (h *Handler) readManifest(ctx context.Context, dg string) (manifestDoc, int64, bool) {
	x, ok := hexOf(dg)
	if !ok {
		return manifestDoc{}, 0, false
	}
	b, err := h.driver.GetContent(ctx, blobDataPath(x))
	if err != nil {
		return manifestDoc{}, 0, false
	}
	var m manifestDoc
	if json.Unmarshal(b, &m) != nil {
		return manifestDoc{}, 0, false
	}
	return m, int64(len(b)), true
}

// revisionOf legge l'etichetta revision dal config blob del manifest dg. «» se il manifest non
// ha un config (un indice), se il blob manca o se l'etichetta non è un commit: nessun errore,
// il campo resta assente.
func (h *Handler) revisionOf(ctx context.Context, dg string) string {
	m, _, ok := h.readManifest(ctx, dg)
	if !ok || m.Config == nil {
		return ""
	}
	x, ok := hexOf(m.Config.Digest)
	if !ok {
		return ""
	}
	b, err := h.driver.GetContent(ctx, blobDataPath(x))
	if err != nil {
		return ""
	}
	var cfg struct {
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"config"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return ""
	}
	if rev := cfg.Config.Labels[RevisionLabel]; revisionRe.MatchString(rev) {
		return rev
	}
	return ""
}

// imageSize somma i byte unici dell'immagine: manifest, config, layer e, per un indice, le
// immagini che richiama (profondità limitata).
func (h *Handler) imageSize(ctx context.Context, dg string) int64 {
	seen := map[string]bool{}
	var total int64
	var walk func(dg string, depth int)
	walk = func(dg string, depth int) {
		if seen[dg] || depth > 4 {
			return
		}
		seen[dg] = true
		m, size, ok := h.readManifest(ctx, dg)
		if !ok {
			return
		}
		total += size
		if m.Config != nil && !seen[m.Config.Digest] {
			seen[m.Config.Digest] = true
			total += m.Config.Size
		}
		for _, l := range m.Layers {
			if !seen[l.Digest] {
				seen[l.Digest] = true
				total += l.Size
			}
		}
		for _, c := range m.Manifests {
			walk(c.Digest, depth+1)
		}
	}
	walk(dg, 0)
	return total
}

func (h *Handler) kindOf(ctx context.Context, dg string) string {
	m, _, ok := h.readManifest(ctx, dg)
	if ok && m.Config != nil && strings.Contains(m.Config.MediaType, "helm") {
		return "helm"
	}
	return "image"
}

func techTag(dg, suffix string) string {
	return strings.Replace(dg, ":", "-", 1) + suffix
}

// verifySignature dice se per il digest esiste una firma cosign valida con la chiave
// dell'istanza: l'artefatto «sha256-<hex>.sig» ha nel layer il payload simplesigning e, nella
// annotation, la firma ECDSA del suo SHA-256; il payload deve nominare lo stesso digest.
// «Nessuna firma» e «firma non valida» sono entrambe unverified.
func (h *Handler) verifySignature(ctx context.Context, tags map[string]string, dg string) bool {
	if h.signingKey == nil {
		return false
	}
	sigDigest, ok := tags[techTag(dg, ".sig")]
	if !ok {
		return false
	}
	m, _, ok := h.readManifest(ctx, sigDigest)
	if !ok || len(m.Layers) != 1 {
		return false
	}
	return h.verifyCosign(ctx, &h.signingKey.PublicKey, m.Layers[0], dg)
}

func (h *Handler) verifyCosign(ctx context.Context, pub *ecdsa.PublicKey, layer descriptor, subject string) bool {
	x, ok := hexOf(layer.Digest)
	if !ok {
		return false
	}
	payload, err := h.driver.GetContent(ctx, blobDataPath(x))
	if err != nil {
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(layer.Annotations["dev.cosignproject.cosign/signature"])
	if err != nil {
		return false
	}
	sum := sha256.Sum256(payload)
	if !ecdsa.VerifyASN1(pub, sum[:], sig) {
		return false
	}
	var p simpleSigning
	if json.Unmarshal(payload, &p) != nil {
		return false
	}
	return p.Critical.Image.DockerManifestDigest == subject
}

// hasSbom: c'è un SBOM o un'attestazione per quel digest.
//
// Tre forme: il tag «sha256-<hex>.sbom» (quello che crea il registry per le immagini del job),
// il tag «.att» (attestazione) e l'indice dei referrer di fallback «sha256-<hex>» che un
// client OCI aggiorna quando pubblica un artefatto con subject, se elenca un artefatto SBOM.
func (h *Handler) hasSbom(ctx context.Context, tags map[string]string, dg string) bool {
	for _, suffix := range []string{".sbom", ".att"} {
		if _, ok := tags[techTag(dg, suffix)]; ok {
			return true
		}
	}
	idx, ok := tags[techTag(dg, "")]
	if !ok {
		return false
	}
	m, _, ok := h.readManifest(ctx, idx)
	if !ok {
		return false
	}
	for _, d := range m.Manifests {
		t := strings.ToLower(d.ArtifactType)
		if strings.Contains(t, "sbom") || strings.Contains(t, "spdx") || strings.Contains(t, "cyclonedx") {
			return true
		}
	}
	return false
}

// ListPackages elenca le immagini del repo con almeno un tag. Un repo sconosciuto a core dà
// ErrPackageNotFound.
func (h *Handler) ListPackages(ctx context.Context, repoID string) ([]Package, error) {
	if !uuidRe.MatchString(repoID) {
		return nil, ErrPackageNotFound
	}
	st, err := h.packageSettings(ctx, repoID)
	if err != nil {
		return nil, err
	}
	removedByImage := map[string][]RemovedTag{}
	if h.settings != nil {
		removed, err := h.settings.src.RemovedTags(ctx, repoID)
		if err != nil {
			return nil, err
		}
		for _, tag := range removed {
			removedByImage[tag.Image] = append(removedByImage[tag.Image], tag)
		}
	}
	names, err := h.imageNames(ctx, repoID)
	if err != nil {
		return nil, err
	}
	out := []Package{}
	revs := map[string]string{} // digest → etichetta revision, letta una volta per digest
	for _, name := range names {
		all, err := h.tagsOf(ctx, name)
		if err != nil {
			return nil, err
		}
		pkg := Package{Image: strings.TrimPrefix(strings.TrimPrefix(name, repoID), "/"), Tags: []string{}, TagDetails: []PackageTag{}}
		pkg.RemovedTags = removedByImage[pkg.Image]
		for t, dg := range all {
			if _, aux := provenanceSubject(t); aux {
				continue
			}
			pushed, ok, err := h.modTime(ctx, tagLinkPath(name, t))
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			rev, seen := revs[dg]
			if !seen {
				rev = h.revisionOf(ctx, dg)
				revs[dg] = rev
			}
			pkg.TagDetails = append(pkg.TagDetails, PackageTag{
				Name: t, Digest: dg, SizeBytes: h.imageSize(ctx, dg), PushedAt: pushed.UTC(),
				Signature: signatureValue(h.verifySignature(ctx, all, dg)),
				HasSbom:   h.hasSbom(ctx, all, dg), Protected: st.protected(t), PushedBy: h.readPusher(ctx, name, t),
				Revision: rev,
			})
		}
		if len(pkg.TagDetails) == 0 {
			continue
		}
		sort.Slice(pkg.TagDetails, func(i, j int) bool {
			a, b := pkg.TagDetails[i], pkg.TagDetails[j]
			if !a.PushedAt.Equal(b.PushedAt) {
				return a.PushedAt.After(b.PushedAt)
			}
			return a.Name < b.Name
		})
		for _, t := range pkg.TagDetails {
			pkg.Tags = append(pkg.Tags, t.Name)
		}
		latest := pkg.TagDetails[0]
		pkg.Digest, pkg.SizeBytes, pkg.UpdatedAt = latest.Digest, latest.SizeBytes, latest.PushedAt
		pkg.Signature, pkg.HasSbom, pkg.PushedBy = latest.Signature, latest.HasSbom, latest.PushedBy
		pkg.Kind = h.kindOf(ctx, latest.Digest)
		out = append(out, pkg)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Image < out[j].Image })
	return out, nil
}

// ImageRevision dà l'etichetta revision (H14, GIT-395) del tag o del digest richiesto di
// un'immagine, leggendo solo i link dei tag, il manifest e il config blob di quell'immagine.
// «» se l'etichetta manca o non è un commit. ErrPackageNotFound se l'immagine o il tag non
// ci sono; ErrInvalidImage se il nome non è valido. Con digest e tag vale il digest.
func (h *Handler) ImageRevision(ctx context.Context, repoID, image, tag, digest string) (string, error) {
	if !uuidRe.MatchString(repoID) {
		return "", ErrPackageNotFound
	}
	name := repoID
	if image != "" {
		for _, seg := range strings.Split(image, "/") {
			if !imageSegRe.MatchString(seg) {
				return "", ErrInvalidImage
			}
		}
		name = repoID + "/" + image
	}
	if digest != "" {
		if _, ok := hexOf(digest); !ok {
			return "", ErrPackageNotFound
		}
		all, err := h.tagsOf(ctx, name)
		if err != nil {
			return "", err
		}
		for t, dg := range all {
			if _, aux := provenanceSubject(t); !aux && dg == digest {
				return h.revisionOf(ctx, dg), nil
			}
		}
		return "", ErrPackageNotFound
	}
	if !tagRe.MatchString(tag) {
		return "", ErrPackageNotFound
	}
	if _, aux := provenanceSubject(tag); aux {
		return "", ErrPackageNotFound
	}
	dg, err := h.tagDigest(ctx, name, tag)
	if err != nil {
		return "", err
	}
	if dg == "" {
		return "", ErrPackageNotFound
	}
	return h.revisionOf(ctx, dg), nil
}

func signatureValue(ok bool) string {
	if ok {
		return SignatureVerified
	}
	return SignatureUnverified
}

// packageSettings legge le impostazioni del repo; senza fonte (test, registry isolato) sono
// vuote: nessun tag protetto.
func (h *Handler) packageSettings(ctx context.Context, repoID string) (Settings, error) {
	if h.settings == nil {
		return Settings{RepoID: repoID}, nil
	}
	st, err := h.settings.get(ctx, repoID)
	if errors.Is(err, ErrSettingsNotFound) {
		return Settings{}, ErrPackageNotFound
	}
	return st, err
}

// DeletePackage elimina un tag (tag non vuoto) o l'intera immagine con tutti i suoi tag. Un
// tag protetto dà ErrProtectedTag, e con l'immagine intera non si elimina niente se anche uno
// solo lo è. L'eliminazione passa da distribution (Untag e Delete del manifest); firme e SBOM
// dell'immagine seguono il manifest. I blob li raccoglie la pulizia.
func (h *Handler) DeletePackage(ctx context.Context, repoID, image, tag string) error {
	if !uuidRe.MatchString(repoID) {
		return ErrPackageNotFound
	}
	name := repoID
	if image != "" {
		for _, seg := range strings.Split(image, "/") {
			if !imageSegRe.MatchString(seg) {
				return ErrInvalidImage
			}
		}
		name = repoID + "/" + image
	}
	if tag != "" && !tagRe.MatchString(tag) {
		return ErrPackageNotFound
	}
	st, err := h.packageSettings(ctx, repoID)
	if err != nil {
		return err
	}
	if h.cleanup != nil {
		h.cleanup.mu.Lock()
		defer h.cleanup.mu.Unlock()
	}
	all, err := h.tagsOf(ctx, name)
	if err != nil {
		return err
	}
	var drop []string
	for t := range all {
		if _, aux := provenanceSubject(t); aux {
			continue
		}
		if tag == "" || t == tag {
			drop = append(drop, t)
		}
	}
	if len(drop) == 0 {
		return ErrPackageNotFound
	}
	for _, t := range drop {
		if st.protected(t) {
			return fmt.Errorf("%w: %s", ErrProtectedTag, t)
		}
	}
	digests := map[string]bool{}
	for _, t := range drop {
		digests[all[t]] = true
		if err := h.appDelete(ctx, repoID, name, t); err != nil {
			return err
		}
		delete(all, t)
		_ = h.driver.Delete(ctx, pusherPath(name, t))
	}
	// Un manifest ancora raggiunto da un altro tag resta; gli altri si eliminano con firma e SBOM.
	still := map[string]bool{}
	for t, dg := range all {
		if _, aux := provenanceSubject(t); !aux {
			still[dg] = true
		}
	}
	for dg := range digests {
		if still[dg] {
			continue
		}
		for _, suffix := range []string{".sig", ".sbom", ".att", ""} {
			at := techTag(dg, suffix)
			adg, ok := all[at]
			if !ok {
				continue
			}
			if err := h.appDelete(ctx, repoID, name, at); err != nil {
				return err
			}
			delete(all, at)
			if err := h.appDelete(ctx, repoID, name, adg); err != nil {
				return err
			}
		}
		if err := h.appDelete(ctx, repoID, name, dg); err != nil {
			return err
		}
	}
	h.invalidateUsage()
	return nil
}

// appDelete elimina un tag o un manifest passando da distribution, con un permesso di
// eliminazione sul solo repo: la stessa app che serve le richieste, quindi la stessa cache.
func (h *Handler) appDelete(ctx context.Context, repoID, name, ref string) error {
	g := &grant{user: "core", repoID: repoID, actions: map[string]bool{actionDelete: true, actionPull: true}}
	req := httptest.NewRequest(http.MethodDelete, "http://registry/v2/"+name+"/manifests/"+ref, nil).WithContext(context.WithValue(ctx, grantKey{}, g))
	rec := httptest.NewRecorder()
	h.app.ServeHTTP(rec, req)
	switch rec.Code {
	case http.StatusAccepted, http.StatusNotFound:
		return nil
	default:
		return fmt.Errorf("registry: eliminazione di %s: %d", ref, rec.Code)
	}
}
