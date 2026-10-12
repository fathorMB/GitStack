package upgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Limiti dei download: il tarball del repository è di pochi MB.
const (
	maxAPIBody  = 4 << 20
	maxTarball  = 256 << 20
	maxBinary   = 256 << 20
	maxChartDir = "deploy/gitstack/"
)

var (
	hexRe = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	// refRe ammette nomi di tag e di ramo semplici: finiscono in un URL.
	refRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,100}$`)
)

// Target è la destinazione risolta.
type Target struct {
	// Ref è quello che ha scritto l'utente (o il ramo di default).
	Ref string
	// SHA è il commit completo.
	SHA string
	// Tag è il tag immagine e il nome della release GitHub: sha-<commit> o
	// il nome del tag di versione.
	Tag string
	// K3sVersion è la versione di k3s dichiarata dal commit in
	// K3sVersionFile ("" se non la dichiara): la compila FetchChart.
	K3sVersion string
}

func (o *Options) get(ctx context.Context, rawURL string, limit int64, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := o.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", rawURL, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s: risposta troppo grande", rawURL)
	}
	return b, nil
}

func (o *Options) apiCommitSHA(ctx context.Context, ref string) (string, error) {
	b, err := o.get(ctx, fmt.Sprintf("%s/repos/%s/commits/%s", o.apiBase(), o.repo(), url.PathEscape(ref)), maxAPIBody, "application/vnd.github+json")
	if err != nil {
		return "", err
	}
	var r struct {
		SHA string `json:"sha"`
	}
	if err := json.Unmarshal(b, &r); err != nil || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(r.SHA) {
		return "", fmt.Errorf("risposta inattesa dall'API per %s", ref)
	}
	return r.SHA, nil
}

// ResolveTarget traduce --to (versione, sha corto o intero, sha-<commit>, o
// vuoto = ultimo commit del ramo di default) in commit e tag.
func (o *Options) ResolveTarget(ctx context.Context, to string) (*Target, error) {
	ref := strings.TrimSpace(to)
	if ref == "" {
		ref = o.ref()
	}
	t := &Target{Ref: ref}
	lookup := ref
	isSHA := false
	if h, ok := strings.CutPrefix(ref, "sha-"); ok {
		lookup, isSHA = h, true
	} else if hexRe.MatchString(ref) {
		isSHA = true
	}
	if isSHA && !hexRe.MatchString(lookup) {
		return nil, fmt.Errorf("%q non è uno sha di commit", ref)
	}
	if !refRe.MatchString(lookup) {
		return nil, fmt.Errorf("destinazione %q non valida", ref)
	}
	sha, err := o.apiCommitSHA(ctx, lookup)
	if err != nil {
		return nil, fmt.Errorf("la destinazione %q non esiste in %s (o GitHub non risponde): %w", ref, o.repo(), err)
	}
	t.SHA = sha
	if isSHA || ref == o.ref() {
		t.Tag = "sha-" + sha
	} else {
		t.Tag = ref
	}
	return t, nil
}

// currentRef è il riferimento da confrontare con la destinazione: lo sha
// del tag immagine installato.
func currentRef(imageTag string) string {
	if h, ok := strings.CutPrefix(imageTag, "sha-"); ok {
		return h
	}
	return imageTag
}

// Direction è il rapporto fra versione installata e destinazione.
type Direction int

// Valori di Direction.
const (
	Ahead     Direction = iota // la destinazione segue la versione installata
	Identical                  // stessa versione
	Behind                     // la destinazione è più vecchia: downgrade
	Diverged                   // storie diverse: non è un aggiornamento
)

// Compare confronta la versione installata con la destinazione.
func (o *Options) Compare(ctx context.Context, t *Target) (Direction, error) {
	cur := currentRef(o.Cfg.ImageTag)
	if !refRe.MatchString(cur) {
		return Diverged, fmt.Errorf("image_tag %q del config non riconoscibile", o.Cfg.ImageTag)
	}
	if o.Cfg.ImageTag == t.Tag {
		return Identical, nil
	}
	u := fmt.Sprintf("%s/repos/%s/compare/%s...%s", o.apiBase(), o.repo(), url.PathEscape(cur), t.SHA)
	b, err := o.get(ctx, u, maxAPIBody, "application/vnd.github+json")
	if err != nil {
		return Diverged, fmt.Errorf("non riesco a confrontare la versione installata (%s) con la destinazione: %w", o.Cfg.ImageTag, err)
	}
	var r struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return Diverged, fmt.Errorf("risposta del confronto non valida: %w", err)
	}
	switch r.Status {
	case "ahead":
		return Ahead, nil
	case "identical":
		return Identical, nil
	case "behind":
		return Behind, nil
	case "diverged":
		return Diverged, nil
	}
	return Diverged, fmt.Errorf("stato del confronto inatteso: %q", r.Status)
}

// FetchBinary scarica il binario della release, il checksum SHA-256 e il
// bundle cosign (.sigstore.json). Verifica SHA-256, scarica e verifica la
// firma (V5) prima di scrivere in dest.
func (o *Options) FetchBinary(ctx context.Context, t *Target, dest string) (string, error) {
	base := fmt.Sprintf("%s/%s/releases/download/%s/gitstack-linux-amd64", o.releaseBase(), o.repo(), url.PathEscape(t.Tag))
	sum, err := o.get(ctx, base+".sha256", 4096, "")
	if err != nil {
		return "", fmt.Errorf("checksum del binario non disponibile: %w", err)
	}
	fields := strings.Fields(string(sum))
	if len(fields) == 0 {
		return "", errors.New("file .sha256 vuoto")
	}
	want := strings.ToLower(fields[0])
	if len(want) != 64 {
		return "", fmt.Errorf("checksum del binario non valido: %q", want)
	}
	if _, err := hex.DecodeString(want); err != nil {
		return "", fmt.Errorf("checksum del binario non valido: %q", want)
	}
	bin, err := o.get(ctx, base, maxBinary, "")
	if err != nil {
		return "", fmt.Errorf("binario di gitstack non disponibile: %w", err)
	}
	got := sha256.Sum256(bin)
	if hex.EncodeToString(got[:]) != want {
		return "", fmt.Errorf("il binario scaricato non corrisponde al checksum SHA-256: atteso %s, trovato %s. Non installato", want, hex.EncodeToString(got[:]))
	}
	if err := os.WriteFile(dest, bin, 0o755); err != nil { //nolint:gosec // eseguibile
		return "", err
	}

	// Scarica il bundle cosign (.sigstore.json) accanto al binario.
	// Senza il bundle la verifica della firma non può avvenire (V5):
	// è il fix del fail-open, CTO GIT-209.
	bundleURL := base + ".sigstore.json"
	bundle, err := o.get(ctx, bundleURL, 64<<10, "")
	if err != nil {
		if o.V5.AllowUnverified {
			// N9: eccezione per mirror non firmati.
			return want, nil
		}
		return "", fmt.Errorf("%w (per %s, URL %s)", ErrBundleMissing, dest, bundleURL)
	}
	if err := os.WriteFile(dest+".sigstore.json", bundle, 0o644); err != nil {
		return "", err
	}

	// Verifica firma cosign V5.
	if err := o.V5.VerifyBinary(ctx, dest); err != nil {
		return "", fmt.Errorf("verifica firma cosign fallita: %w", err)
	}
	return want, nil
}

// FetchChart scarica il chart .tgz dalla release GitHub firmata con cosign,
// ne verifica la firma con il bundle (.sigstore.json) e lo estrae in dest.
// La verifica avviene PRIMA dell'estrazione: il .tgz pubblicato dalla CI ha
// byte identici a quelli firmati (CTO GIT-209).
// Legge anche k3s-version dal sorgente codeload per t.K3sVersion.
func (o *Options) FetchChart(ctx context.Context, t *Target, dest string) error {
	// Scarica il chart dalla release GitHub: solo il .tgz pubblicato ha
	// i byte firmati. Il nome del file è gitstack-<versione>.tgz dove
	// la versione si legge dal Chart.yaml estratto dal codeload prima.
	// Prima leggiamo la versione dal sorgente (per il nome del file).
	codeloadURL := fmt.Sprintf("%s/%s/tar.gz/%s", o.codeloadBase(), o.repo(), t.SHA)
	src, err := o.get(ctx, codeloadURL, maxTarball, "")
	if err != nil {
		return fmt.Errorf("sorgente del chart non disponibile: %w", err)
	}
	// Estrae la versione dal Chart.yaml nel sorgente.
	chartVersion := chartVersionFromSource(src)
	if chartVersion == "" {
		return fmt.Errorf("versione del chart non trovata nel sorgente")
	}

	chartURL := fmt.Sprintf("%s/%s/releases/download/%s/gitstack-%s.tgz",
		o.releaseBase(), o.repo(), url.PathEscape(t.Tag), chartVersion)
	chart, err := o.get(ctx, chartURL, maxBinary, "")
	if err != nil {
		return fmt.Errorf("chart firmato non disponibile: %w", err)
	}

	// Scarica il bundle cosign (.sigstore.json) del chart.
	// Senza il bundle la verifica della firma non può avvenire (V5):
	// fix del fail-open, CTO GIT-209.
	bundleURL := chartURL + ".sigstore.json"
	bundle, err := o.get(ctx, bundleURL, 64<<10, "")
	if err != nil {
		if o.V5.AllowUnverified {
			// N9: eccezione per mirror non firmati, estrai senza verifica.
			if err := os.MkdirAll(dest, 0o755); err != nil { //nolint:gosec
				return err
			}
			return extractTGZ(bytes.NewReader(chart), dest)
		}
		return fmt.Errorf("%w (chart %s, bundle %s)", ErrBundleMissing, chartURL, bundleURL)
	}

	// Verifica firma cosign del chart.
	tmpChart, err := os.CreateTemp("", "gitstack-chart-*.tgz")
	if err != nil {
		return err
	}
	tmpChartPath := tmpChart.Name()
	tmpBundlePath := tmpChartPath + ".sigstore.json"
	if _, err := tmpChart.Write(chart); err != nil {
		tmpChart.Close()
		os.Remove(tmpChartPath)
		return err
	}
	if err := tmpChart.Close(); err != nil {
		os.Remove(tmpChartPath)
		return err
	}
	if err := os.WriteFile(tmpBundlePath, bundle, 0o644); err != nil {
		os.Remove(tmpChartPath)
		os.Remove(tmpBundlePath)
		return err
	}

	if err := o.V5.VerifyChart(ctx, tmpChartPath); err != nil {
		os.Remove(tmpChartPath)
		os.Remove(tmpBundlePath)
		return fmt.Errorf("verifica firma cosign chart fallita: %w", err)
	}
	// Verifica passata: elimina temporanei.
	_ = os.Remove(tmpBundlePath)
	_ = os.Remove(tmpChartPath)

	if err := os.MkdirAll(dest, 0o755); err != nil { //nolint:gosec // come il chart installato
		return err
	}
	if err := extractTGZ(bytes.NewReader(chart), dest); err != nil {
		return err
	}

	// Legge k3s-version dal sorgente codeload.
	if err := o.readK3sVersion(ctx, t, src); err != nil {
		return err
	}
	return nil
}

// chartVersionFromSource estrae la versione da Chart.yaml in un tar.gz codeload.
func chartVersionFromSource(src []byte) string {
	zr, err := gzip.NewReader(bytes.NewReader(src))
	if err != nil {
		return ""
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return ""
		}
		_, rel, ok := strings.Cut(h.Name, "/")
		if !ok {
			continue
		}
		if rel == "deploy/gitstack/Chart.yaml" && h.Typeflag == tar.TypeReg {
			b, err := io.ReadAll(io.LimitReader(tr, 4096))
			if err != nil {
				return ""
			}
			for _, line := range strings.Split(string(b), "\n") {
				if strings.HasPrefix(line, "version:") {
					return strings.TrimSpace(strings.TrimPrefix(line, "version:"))
				}
			}
		}
	}
	return ""
}

// readK3sVersion legge k3s-version dal sorgente codeload e lo scrive in t.K3sVersion.
func (o *Options) readK3sVersion(ctx context.Context, t *Target, src []byte) error {
	zr, err := gzip.NewReader(strings.NewReader(string(src)))
	if err != nil {
		return nil // non bloccante
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil
		}
		_, rel, ok := strings.Cut(h.Name, "/")
		if !ok {
			continue
		}
		if rel == K3sVersionFile && h.Typeflag == tar.TypeReg {
			b, err := io.ReadAll(io.LimitReader(tr, 1024))
			if err != nil {
				return nil
			}
			v, err := ParseK3sVersion(string(b))
			if err != nil {
				return nil
			}
			t.K3sVersion = v.String()
			return nil
		}
	}
	return nil
}

// extractTGZ estrae un tarball gzip in una directory.
func extractTGZ(r io.Reader, dest string) error {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		clean := path.Clean(h.Name)
		if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
			continue
		}
		target := filepath.Join(dest, filepath.FromSlash(clean))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, io.LimitReader(tr, 64<<20)); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}
	return nil
}

var imageLineRe = regexp.MustCompile(`(?m)^\s*-?\s*image:\s*["']?([^"'\s]+)["']?\s*$`)

// imagesFromManifests estrae le immagini (uniche, in ordine) dai manifest
// di `helm template`.
func imagesFromManifests(out string) []string {
	seen := map[string]bool{}
	var res []string
	for _, m := range imageLineRe.FindAllStringSubmatch(out, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			res = append(res, m[1])
		}
	}
	return res
}

// CheckImage verifica che l'immagine esista nel suo registry (API
// distribution v2, con il token anonimo dei registry pubblici).
func (o *Options) CheckImage(ctx context.Context, image string) error {
	if o.ImageCheck != nil {
		return o.ImageCheck(ctx, image)
	}
	host, repo, ref, err := splitImage(image)
	if err != nil {
		return err
	}
	scheme := "https"
	if strings.HasPrefix(host, "localhost") || strings.HasPrefix(host, "127.0.0.1") {
		scheme = "http"
	}
	u := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", scheme, host, repo, ref)
	token := ""
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", strings.Join([]string{
			"application/vnd.oci.image.index.v1+json", "application/vnd.oci.image.manifest.v1+json",
			"application/vnd.docker.distribution.manifest.list.v2+json", "application/vnd.docker.distribution.manifest.v2+json",
		}, ", "))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := o.client().Do(req)
		if err != nil {
			return fmt.Errorf("registry %s non raggiungibile: %w", host, err)
		}
		_ = resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusOK:
			return nil
		case http.StatusNotFound:
			return fmt.Errorf("immagine %s non trovata nel registry", image)
		case http.StatusUnauthorized:
			if attempt == 1 {
				return fmt.Errorf("immagine %s: accesso negato dal registry (immagine inesistente o privata)", image)
			}
			if token, err = o.registryToken(ctx, resp.Header.Get("Www-Authenticate")); err != nil {
				return fmt.Errorf("immagine %s: %w", image, err)
			}
		default:
			return fmt.Errorf("immagine %s: il registry risponde HTTP %d", image, resp.StatusCode)
		}
	}
	return nil
}

var authParamRe = regexp.MustCompile(`(\w+)="([^"]*)"`)

func (o *Options) registryToken(ctx context.Context, challenge string) (string, error) {
	if !strings.HasPrefix(strings.ToLower(challenge), "bearer ") {
		return "", errors.New("il registry chiede un'autenticazione non supportata")
	}
	p := map[string]string{}
	for _, m := range authParamRe.FindAllStringSubmatch(challenge, -1) {
		p[strings.ToLower(m[1])] = m[2]
	}
	realm, err := url.Parse(p["realm"])
	if err != nil || (realm.Scheme != "https" && realm.Scheme != "http") {
		return "", errors.New("sfida di autenticazione del registry non valida")
	}
	q := realm.Query()
	if p["service"] != "" {
		q.Set("service", p["service"])
	}
	if p["scope"] != "" {
		q.Set("scope", p["scope"])
	}
	realm.RawQuery = q.Encode()
	b, err := o.get(ctx, realm.String(), maxAPIBody, "")
	if err != nil {
		return "", fmt.Errorf("token del registry: %w", err)
	}
	var r struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return "", fmt.Errorf("token del registry non valido: %w", err)
	}
	if r.Token != "" {
		return r.Token, nil
	}
	if r.AccessToken != "" {
		return r.AccessToken, nil
	}
	return "", errors.New("il registry non ha dato un token")
}

// splitImage scompone registry/percorso:tag (o @digest).
func splitImage(image string) (host, repo, ref string, err error) {
	name := image
	ref = "latest"
	if n, d, ok := strings.Cut(image, "@"); ok {
		name, ref = n, d
	} else if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
		name, ref = image[:i], image[i+1:]
	}
	first, rest, ok := strings.Cut(name, "/")
	if ok && (strings.ContainsAny(first, ".:") || first == "localhost") {
		host, repo = first, rest
	} else {
		host, repo = "docker.io", name
	}
	if host == "docker.io" {
		host = "registry-1.docker.io"
		if !strings.Contains(repo, "/") {
			repo = "library/" + repo
		}
	}
	if repo == "" || ref == "" {
		return "", "", "", fmt.Errorf("riferimento immagine non valido: %q", image)
	}
	return host, repo, ref, nil
}
