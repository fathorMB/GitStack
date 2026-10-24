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
	// versionTagRe è un tag di rilascio SemVer (GIT-222): vX.Y.Z, con
	// prerelease (-rc.1, -test.2) e senza build metadata. Gli stessi tag che
	// fanno partire il job `release` della CI.
	versionTagRe = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
)

// IsVersionTag dice se s è un tag di rilascio vX.Y.Z(-prerelease).
func IsVersionTag(s string) bool { return versionTagRe.MatchString(s) }

// Target è la destinazione risolta.
type Target struct {
	// Ref è quello che ha scritto l'utente (o il ramo di default).
	Ref string
	// SHA è il commit completo.
	SHA string
	// Tag è il tag immagine e il nome della release GitHub: sha-<commit> o
	// il nome del tag di versione.
	Tag string
	// Version è la versione del chart e dell'appVersion pubblicati con il
	// rilascio (il tag senza "v"); "" per una build sha-<commit>, che usa la
	// versione scritta in Chart.yaml del commit.
	Version string
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
	if IsVersionTag(ref) {
		t.Version = strings.TrimPrefix(ref, "v")
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

// FetchBinary scarica il binario della release e ne verifica lo SHA-256
// (V5) prima di scriverlo in dest.
func (o *Options) FetchBinary(ctx context.Context, t *Target, dest string) (string, error) {
	base := o.assetBase(t.Tag) + "/gitstack-linux-amd64"
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
	// V5: firma cosign del binario, col bundle pubblicato accanto (stessa
	// release). Senza bundle o con una firma che non torna, niente si installa.
	if o.v5().AllowUnsigned {
		o.logf("    AVVISO: firma del binario NON verificata (eccezione N9 attiva)")
		return want, nil
	}
	bundle := dest + ".sigstore.json"
	if err := o.fetchBundle(ctx, base+".sigstore.json", bundle); err != nil {
		_ = os.Remove(dest)
		return "", err
	}
	defer func() { _ = os.Remove(bundle) }()
	if err := o.v5().VerifyBlob(ctx, dest, bundle); err != nil {
		_ = os.Remove(dest)
		return "", fmt.Errorf("binario di gitstack: %w. Non installato. Per un mirror interno non firmato serve l'eccezione esplicita --allow-unsigned-mirrors (N9)", err)
	}
	o.logf("    firma cosign del binario verificata")
	return want, nil
}

// assetBase è la cartella degli asset di una release (binario, chart, bundle):
// la release GitHub, o con network.download_mirror <mirror>/gitstack/<tag>
// (stessa struttura di deploy/install.sh). Cosa arriva da lì si verifica come
// ciò che arriva da GitHub: stesso bundle, stessa identità.
func (o *Options) assetBase(tag string) string {
	if m := strings.TrimRight(o.DownloadMirror, "/"); m != "" {
		return m + "/gitstack/" + url.PathEscape(tag)
	}
	return fmt.Sprintf("%s/%s/releases/download/%s", o.releaseBase(), o.repo(), url.PathEscape(tag))
}

// sourceURL è il tarball del sorgente del commit (GitHub o mirror).
func (o *Options) sourceURL(sha string) string {
	if m := strings.TrimRight(o.DownloadMirror, "/"); m != "" {
		return m + "/gitstack/source/" + sha + ".tar.gz"
	}
	return fmt.Sprintf("%s/%s/tar.gz/%s", o.codeloadBase(), o.repo(), sha)
}

// fetchBundle scarica il bundle cosign in dest. Se non c'è è ErrBundleMissing.
func (o *Options) fetchBundle(ctx context.Context, rawURL, dest string) error {
	b, err := o.get(ctx, rawURL, 1<<20, "")
	if err != nil {
		return fmt.Errorf("%w (%v). La release deve contenere il bundle accanto all'artefatto: la CI lo pubblica nel job di firma; per un mirror interno non firmato serve --allow-unsigned-mirrors (N9)", ErrBundleMissing, err)
	}
	return os.WriteFile(dest, b, 0o600)
}

// FetchChart scarica il sorgente del commit ed estrae deploy/gitstack in
// dest (cartella nuova).
func (o *Options) FetchChart(ctx context.Context, t *Target, dest string) error {
	b, err := o.get(ctx, o.sourceURL(t.SHA), maxTarball, "")
	if err != nil {
		return fmt.Errorf("chart non disponibile: %w", err)
	}
	zr, err := gzip.NewReader(strings.NewReader(string(b)))
	if err != nil {
		return fmt.Errorf("sorgente scaricato non valido: %w", err)
	}
	tr := tar.NewReader(zr)
	if err := os.MkdirAll(dest, 0o755); err != nil { //nolint:gosec // come il chart installato
		return err
	}
	n := 0
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("sorgente scaricato non valido: %w", err)
		}
		// Il primo componente è <repo>-<sha>/.
		_, rel, ok := strings.Cut(h.Name, "/")
		if !ok {
			continue
		}
		if rel == K3sVersionFile && h.Typeflag == tar.TypeReg {
			b, err := io.ReadAll(io.LimitReader(tr, 1024))
			if err != nil {
				return fmt.Errorf("sorgente scaricato non valido: %w", err)
			}
			v, err := ParseK3sVersion(string(b))
			if err != nil {
				return fmt.Errorf("%s del commit %s: %w", K3sVersionFile, t.SHA, err)
			}
			t.K3sVersion = v.String()
			continue
		}
		sub, ok := strings.CutPrefix(rel, maxChartDir)
		if !ok || sub == "" {
			continue
		}
		clean := path.Clean(sub)
		if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
			return fmt.Errorf("percorso sospetto nel sorgente: %q", h.Name)
		}
		target := filepath.Join(dest, filepath.FromSlash(clean))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil { //nolint:gosec // come il chart installato
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil { //nolint:gosec // come il chart installato
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644) //nolint:gosec // percorso verificato sopra
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, io.LimitReader(tr, 64<<20)); err != nil { //nolint:gosec // limitato
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
			n++
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "Chart.yaml")); err != nil || n == 0 {
		return fmt.Errorf("il commit %s non contiene deploy/gitstack/Chart.yaml", t.SHA)
	}
	if o.v5().AllowUnsigned {
		o.logf("    AVVISO: firma del chart NON verificata (eccezione N9 attiva)")
		return nil
	}
	return o.useSignedChart(ctx, t, dest)
}

// useSignedChart scarica il chart .tgz pubblicato dalla CI nella release (gli
// stessi byte firmati), ne verifica il bundle e solo dopo lo estrae al posto
// del chart ricavato dal sorgente.
func (o *Options) useSignedChart(ctx context.Context, t *Target, dest string) error {
	b, err := os.ReadFile(filepath.Join(dest, "Chart.yaml"))
	if err != nil {
		return err
	}
	version := ""
	for _, l := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		if v, ok := strings.CutPrefix(l, "version:"); ok {
			version = strings.Trim(strings.TrimSpace(v), "\"'")
			break
		}
	}
	if t.Version != "" {
		// Rilascio vX.Y.Z: la CI pubblica gitstack-X.Y.Z.tgz con version e
		// appVersion = X.Y.Z, a prescindere dal Chart.yaml del sorgente.
		version = t.Version
	}
	if version == "" || !refRe.MatchString(version) {
		return fmt.Errorf("il Chart.yaml del commit %s non ha una versione valida", t.SHA)
	}
	tmp, err := os.MkdirTemp("", "gitstack-chart-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	tgz := filepath.Join(tmp, "gitstack-"+version+".tgz")
	u := fmt.Sprintf("%s/gitstack-%s.tgz", o.assetBase(t.Tag), version)
	chart, err := o.get(ctx, u, maxTarball, "")
	if err != nil {
		return fmt.Errorf("chart firmato non disponibile nella release %s: %w", t.Tag, err)
	}
	if err := os.WriteFile(tgz, chart, 0o600); err != nil {
		return err
	}
	if err := o.fetchBundle(ctx, u+".sigstore.json", tgz+".sigstore.json"); err != nil {
		return fmt.Errorf("chart %s: %w", u, err)
	}
	if err := o.v5().VerifyBlob(ctx, tgz, tgz+".sigstore.json"); err != nil {
		return fmt.Errorf("chart di %s: %w. Per un mirror interno non firmato serve --allow-unsigned-mirrors (N9)", t.Tag, err)
	}
	o.logf("    firma cosign del chart verificata")
	// Il tgz di helm package ha la radice gitstack/: si estrae in tmp e si
	// sostituisce dest con quella cartella.
	ex := filepath.Join(tmp, "x")
	if err := os.MkdirAll(ex, 0o700); err != nil {
		return err
	}
	if err := extractTGZ(bytes.NewReader(chart), ex); err != nil {
		return fmt.Errorf("chart firmato non valido: %w", err)
	}
	root := filepath.Join(ex, "gitstack")
	signedMeta, err := os.ReadFile(filepath.Join(root, "Chart.yaml")) //nolint:gosec // percorso nostro
	if err != nil {
		return fmt.Errorf("il chart firmato non contiene gitstack/Chart.yaml")
	}
	if t.Version != "" && !chartHasVersion(string(signedMeta), t.Version) {
		return fmt.Errorf("il chart firmato della release %s non ha version e appVersion %s: non è il chart di questa versione", t.Tag, t.Version)
	}
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	return copyTree(root, dest)
}

// chartHasVersion è vero se il Chart.yaml dichiara version e appVersion
// uguali a v (con o senza apici).
func chartHasVersion(chartYAML, v string) bool {
	var ver, app string
	for _, l := range strings.Split(strings.ReplaceAll(chartYAML, "\r\n", "\n"), "\n") {
		if x, ok := strings.CutPrefix(l, "version:"); ok {
			ver = strings.Trim(strings.TrimSpace(x), "\"'")
		}
		if x, ok := strings.CutPrefix(l, "appVersion:"); ok {
			app = strings.Trim(strings.TrimSpace(x), "\"'")
		}
	}
	return ver == v && app == v
}

// copyTree copia una cartella (solo file regolari e cartelle).
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755) //nolint:gosec // come il chart installato
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p) //nolint:gosec // percorso nostro
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644) //nolint:gosec // come il chart installato
	})
}

// extractTGZ estrae un tarball gzip in una cartella (solo file e cartelle,
// con percorsi relativi e senza uscire da dest).
func extractTGZ(r io.Reader, dest string) error {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		clean := path.Clean(h.Name)
		if clean == "." {
			continue
		}
		if clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
			return fmt.Errorf("percorso sospetto nel chart: %q", h.Name)
		}
		target := filepath.Join(dest, filepath.FromSlash(clean))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil { //nolint:gosec // come il chart installato
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil { //nolint:gosec // come il chart installato
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644) //nolint:gosec // percorso verificato sopra
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, io.LimitReader(tr, 64<<20)); err != nil { //nolint:gosec // limitato
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		}
	}
}

var imageLineRe = regexp.MustCompile(`(?m)^\s*-?\s*image:\s*["']?([^"'\s]+)["']?\s*$`)

// ciRunnerImageRe: con la CI accesa l'immagine del runner non e' un `image:` del chart
// (la usa core per creare i pod dei job) ma il valore di GITSTACK_CORE_CI_IMAGE: va
// verificata e controllata come le altre.
var ciRunnerImageRe = regexp.MustCompile(`(?m)^\s*-?\s*name:\s*GITSTACK_CORE_CI_IMAGE\s*\n\s*value:\s*["']?([^"'\s]+)["']?\s*$`)

// imagesFromManifests estrae le immagini (uniche, in ordine) dai manifest
// di `helm template`, compresa quella del runner della CI se accesa.
func imagesFromManifests(out string) []string {
	seen := map[string]bool{}
	var res []string
	out = strings.ReplaceAll(out, "\r\n", "\n")
	for _, re := range []*regexp.Regexp{imageLineRe, ciRunnerImageRe} {
		for _, m := range re.FindAllStringSubmatch(out, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				res = append(res, m[1])
			}
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
