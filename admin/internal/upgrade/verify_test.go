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
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// --- cosign finto -----------------------------------------------------------
//
// Il binario di test fa da cosign: con GITSTACK_FAKE_COSIGN=1 TestMain non
// esegue i test ma si comporta come `cosign verify-blob` / `cosign verify`,
// controllando gli argomenti come farebbe quello vero (identità col regexp,
// issuer, digest del file nel bundle) ed uscendo con 1 o 0. Vale anche su
// Windows, dove uno script non girerebbe.

const (
	fakeCosignEnv     = "GITSTACK_FAKE_COSIGN"
	fakeCosignLogEnv  = "GITSTACK_FAKE_COSIGN_LOG"
	fakeCosignImgsEnv = "GITSTACK_FAKE_COSIGN_IMAGES"
)

func TestMain(m *testing.M) {
	if os.Getenv(fakeCosignEnv) == "1" {
		os.Exit(fakeCosign(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// fakeBundleDoc è il contenuto del bundle finto: cosa è stato firmato e da chi.
type fakeBundleDoc struct {
	SHA256   string `json:"sha256"`
	Identity string `json:"identity"`
	Issuer   string `json:"issuer"`
}

func fakeBundle(signed []byte, identity string) []byte {
	sum := sha256.Sum256(signed)
	b, _ := json.Marshal(fakeBundleDoc{SHA256: hex.EncodeToString(sum[:]), Identity: identity, Issuer: DefaultOIDCIssuer})
	return b
}

func fakeCosign(args []string) int {
	if p := os.Getenv(fakeCosignLogEnv); p != "" {
		if f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			_, _ = fmt.Fprintln(f, strings.Join(args, " "))
			_ = f.Close()
		}
	}
	fail := func(format string, a ...any) int {
		_, _ = fmt.Fprintf(os.Stderr, "Error: "+format+"\n", a...)
		return 1
	}
	if len(args) == 0 {
		return fail("nessun sottocomando")
	}
	cmd, rest := args[0], args[1:]
	var idRe, issuer, bundle string
	var pos []string
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--certificate-identity-regexp":
			i++
			idRe = rest[i]
		case "--certificate-oidc-issuer":
			i++
			issuer = rest[i]
		case "--bundle":
			i++
			bundle = rest[i]
		case "--allow-insecure-registry":
			// ammesso solo verso un mirror http: lo prova il test
		default:
			if strings.HasPrefix(rest[i], "--") {
				return fail("flag non ammesso: %s", rest[i])
			}
			pos = append(pos, rest[i])
		}
	}
	if idRe == "" || issuer == "" || len(pos) != 1 {
		return fail("identità e issuer sono obbligatori (regexp=%q issuer=%q)", idRe, issuer)
	}
	re, err := regexp.Compile(idRe)
	if err != nil {
		return fail("regexp non valido: %v", err)
	}
	var signer fakeBundleDoc
	switch cmd {
	case "verify-blob":
		raw, err := os.ReadFile(bundle)
		if err != nil {
			return fail("bundle: %v", err)
		}
		if err := json.Unmarshal(raw, &signer); err != nil {
			return fail("bundle non valido: %v", err)
		}
		data, err := os.ReadFile(pos[0])
		if err != nil {
			return fail("file: %v", err)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != signer.SHA256 {
			return fail("invalid signature when validating ASN.1 encoded signature: il file non è quello firmato")
		}
	case "verify":
		list, _ := os.ReadFile(os.Getenv(fakeCosignImgsEnv))
		found := false
		for _, l := range strings.Split(string(list), "\n") {
			if img, id, ok := strings.Cut(strings.TrimSpace(l), " "); ok && img == pos[0] {
				signer = fakeBundleDoc{Identity: id, Issuer: DefaultOIDCIssuer}
				found = true
			}
		}
		if !found {
			return fail("no signatures found for image %s", pos[0])
		}
	default:
		return fail("sottocomando inatteso %q", cmd)
	}
	if signer.Issuer != issuer {
		return fail("issuer del certificato %q diverso da %q", signer.Issuer, issuer)
	}
	if !re.MatchString(signer.Identity) {
		return fail("none of the expected identities matched what was in the certificate: %q", signer.Identity)
	}
	return 0
}

// validIdentity è l'identità del workflow di CI del repository di prova (o/r).
const validIdentity = "https://github.com/o/r/.github/workflows/ci.yml@refs/heads/main"

func (w *world) identity() string {
	if w.signer != "" {
		return w.signer
	}
	return validIdentity
}

// signedChartTgz è il chart come lo pubblica la CI (helm package: radice
// gitstack/): byte diversi da quelli del sorgente, ed è l'unico che si firma.
func signedChartTgz(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range [][2]string{
		{"gitstack/Chart.yaml", "name: gitstack\nversion: 0.1.0\n"},
		{"gitstack/values.yaml", "NUOVO: 1\n"},
		{"gitstack/templates/a.yaml", "x"},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: f[0], Mode: 0o644, Size: int64(len(f[1])), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(f[1]))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// useFakeCosign configura il cosign finto per il test: tutto firmato dal
// workflow valido, salvo quello che il test cambia nel world.
func useFakeCosign(t *testing.T, w *world) V5Config {
	t.Helper()
	dir := t.TempDir()
	imgs := filepath.Join(dir, "images.txt")
	image := "mirror.example/fathormb/gitstack-core:sha-" + newSHA
	if err := os.WriteFile(imgs, []byte(image+" "+validIdentity+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(fakeCosignEnv, "1")
	t.Setenv(fakeCosignImgsEnv, imgs)
	t.Setenv(fakeCosignLogEnv, filepath.Join(dir, "calls.log"))
	w.imgSigners = imgs
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return V5Config{CosignPath: exe}
}

func cosignCalls(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(os.Getenv(fakeCosignLogEnv))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// runRefused lancia Run e pretende un rifiuto che contenga want, senza
// aver toccato niente.
func runRefused(t *testing.T, w *world, want ...string) {
	t.Helper()
	res, err := Run(context.Background(), w.o)
	var ref *RefusedError
	if !errors.As(err, &ref) {
		t.Fatalf("atteso RefusedError, ottenuto res=%+v err=%v", res, err)
	}
	if res.Outcome != Refused {
		t.Errorf("esito %v, atteso Refused", res.Outcome)
	}
	for _, s := range want {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("il messaggio %q non contiene %q", err.Error(), s)
		}
	}
	w.assertUntouched()
}

// --- criterio: rifiuti ------------------------------------------------------

func TestV5RunVerificaTuttoEPassa(t *testing.T) {
	w := newWorld(t)
	var log bytes.Buffer
	w.o.Log = &log
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v\n%s", res, err, log.String())
	}
	if got := readFile(t, w.exe); got != newBin {
		t.Errorf("binario non sostituito: %q", got)
	}
	calls := cosignCalls(t)
	var blob, image int
	for _, c := range calls {
		if strings.Contains(c, "insecure") {
			t.Errorf("flag insicuro: %s", c)
		}
		if !strings.Contains(c, "--certificate-identity-regexp ") || !strings.Contains(c, "--certificate-oidc-issuer "+DefaultOIDCIssuer) {
			t.Errorf("identità/issuer non fissati: %s", c)
		}
		switch {
		case strings.HasPrefix(c, "verify-blob "):
			blob++
		case strings.HasPrefix(c, "verify "):
			image++
			if strings.Contains(c, "busybox") {
				t.Errorf("un'immagine di terzi non si verifica: %s", c)
			}
		}
	}
	if blob != 2 || image != 1 {
		t.Errorf("verify-blob=%d (binario e chart), verify=%d (immagine gitstack): %v", blob, image, calls)
	}
	// Il chart installato è quello del tgz firmato.
	if got := readFile(t, filepath.Join(w.chart, "values.yaml")); got != "NUOVO: 1\n" {
		t.Errorf("chart: %q", got)
	}
}

func TestV5RifiutaBinarioSenzaBundle(t *testing.T) {
	w := newWorld(t)
	w.noBundle = true
	runRefused(t, w, "bundle", "non risulta firmato")
}

func TestV5RifiutaBinarioFirmatoDaAltraIdentita(t *testing.T) {
	w := newWorld(t)
	w.signer = "https://github.com/altro/repo/.github/workflows/ci.yml@refs/heads/main"
	runRefused(t, w, "verifica firma cosign fallita", "identities")
}

func TestV5RifiutaWorkflowDiversoStessoRepo(t *testing.T) {
	w := newWorld(t)
	w.signer = "https://github.com/o/r/.github/workflows/altro.yml@refs/heads/main"
	runRefused(t, w, "verifica firma cosign fallita")
}

func TestV5RifiutaBinarioAlterato(t *testing.T) {
	w := newWorld(t)
	// Il bundle firma un altro contenuto: chi altera il binario ne ricalcola
	// anche il .sha256, quindi il checksum torna e solo la firma lo ferma.
	w.binSigned = "BINARIO-ORIGINALE"
	runRefused(t, w, "verifica firma cosign fallita", "Non installato")
}

func TestV5RifiutaChartAlterato(t *testing.T) {
	w := newWorld(t)
	w.chartSign = []byte("chart-originale")
	runRefused(t, w, "chart", "verifica firma cosign fallita")
}

func TestV5RifiutaImmagineNonFirmata(t *testing.T) {
	w := newWorld(t)
	if err := os.WriteFile(w.imgSigners, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	runRefused(t, w, "gitstack-core", "no signatures found")
}

func TestV5RifiutaSeCosignManca(t *testing.T) {
	w := newWorld(t)
	w.o.V5.CosignPath = filepath.Join(t.TempDir(), "non-esiste")
	runRefused(t, w, "cosign non trovato")
}

func TestV5ValoreZeroVerifica(t *testing.T) {
	// Un V5Config vuoto non è «nessuna verifica»: senza bundle rifiuta.
	w := newWorld(t)
	w.o.V5 = V5Config{CosignPath: w.o.V5.CosignPath}
	w.noBundle = true
	runRefused(t, w, "bundle")
}

// --- eccezione N9 -----------------------------------------------------------

func TestV5EccezioneN9AccettaConAvviso(t *testing.T) {
	w := newWorld(t)
	w.noBundle = true
	w.o.V5.AllowUnsigned = true
	if err := os.WriteFile(w.imgSigners, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	w.o.Log = &log
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v\n%s", res, err, log.String())
	}
	if n := strings.Count(log.String(), "N9"); n < 3 {
		t.Errorf("l'avviso N9 deve comparire per binario, chart e immagini (%d):\n%s", n, log.String())
	}
	if calls := cosignCalls(t); len(calls) != 0 {
		t.Errorf("con N9 cosign non va chiamato: %v", calls)
	}
}

// --- identità e immagini ----------------------------------------------------

func TestV5IdentityRegexp(t *testing.T) {
	re := regexp.MustCompile(V5Config{Repo: "fathorMB/GitStack"}.IdentityRegexp())
	for id, want := range map[string]bool{
		"https://github.com/fathorMB/GitStack/.github/workflows/ci.yml@refs/heads/main":    true,
		"https://github.com/fathorMB/GitStack/.github/workflows/ci.yml@refs/tags/v1.2.3":   true,
		"https://github.com/fathorMB/GitStack/.github/workflows/ci.yml@refs/heads/feature": false,
		"https://github.com/fathorMB/GitStack/.github/workflows/altro.yml@refs/heads/main": false,
		"https://github.com/altro/GitStack/.github/workflows/ci.yml@refs/heads/main":       false,
		"https://github.com/fathorMBxGitStack/.github/workflows/ci.yml@refs/heads/main":    false,
		"x https://github.com/fathorMB/GitStack/.github/workflows/ci.yml@refs/heads/main":  false,
	} {
		if got := re.MatchString(id); got != want {
			t.Errorf("%q: %v, atteso %v", id, got, want)
		}
	}
}

func TestIsGitstackImage(t *testing.T) {
	for img, want := range map[string]bool{
		"ghcr.io/fathormb/gitstack-core:sha-abc":      true,
		"mirror.example/x/gitstack-web@sha256:abc":    true,
		"ghcr.io/fathormb/gitstack-gateway":           true,
		"postgres:16":                                 false,
		"nats:2-alpine":                               false,
		"ghcr.io/fathormb/gitstack-core-extra:sha-ab": false,
	} {
		if got := IsGitstackImage(img); got != want {
			t.Errorf("%s: %v, atteso %v", img, got, want)
		}
	}
}

// --- mirror dei download e delle immagini (N9a) -------------------------------

func (w *world) hitsWith(sub string) int {
	w.hitMu.Lock()
	defer w.hitMu.Unlock()
	n := 0
	for _, h := range w.hits {
		if strings.Contains(h, sub) {
			n++
		}
	}
	return n
}

// Con network.download_mirror binario, chart, bundle e sorgente vengono dal
// mirror e si verificano come quelli di GitHub.
func TestV5MirrorDownloadVerificato(t *testing.T) {
	w := newWorld(t)
	w.o.DownloadMirror = w.srv.URL + "/mirror"
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	for _, p := range []string{
		"/mirror/gitstack/sha-" + newSHA + "/gitstack-linux-amd64",
		"/mirror/gitstack/sha-" + newSHA + "/gitstack-linux-amd64.sigstore.json",
		"/mirror/gitstack/sha-" + newSHA + "/gitstack-0.1.0.tgz.sigstore.json",
		"/mirror/gitstack/source/" + newSHA + ".tar.gz",
	} {
		if w.hitsWith(p) == 0 {
			t.Errorf("non richiesto al mirror: %s", p)
		}
	}
	if w.hitsWith("/gh/") != 0 {
		t.Errorf("con il mirror non si va su GitHub: %v", w.hits)
	}
	var blob int
	for _, c := range cosignCalls(t) {
		if strings.HasPrefix(c, "verify-blob ") {
			blob++
		}
	}
	if blob != 2 {
		t.Errorf("verify-blob=%d, attesi 2 (binario e chart dal mirror)", blob)
	}
}

func TestV5MirrorDownloadSenzaBundleRifiutato(t *testing.T) {
	w := newWorld(t)
	w.o.DownloadMirror = w.srv.URL + "/mirror"
	w.noBundle = true
	runRefused(t, w, "bundle", "non risulta firmato")
}

func TestV5MirrorDownloadAlteratoRifiutato(t *testing.T) {
	w := newWorld(t)
	w.o.DownloadMirror = w.srv.URL + "/mirror"
	w.binSigned = "BINARIO-ORIGINALE"
	runRefused(t, w, "verifica firma cosign fallita")
}

func TestV5MirrorDownloadN9(t *testing.T) {
	w := newWorld(t)
	w.o.DownloadMirror = w.srv.URL + "/mirror"
	w.noBundle = true
	w.o.V5.AllowUnsigned = true
	if err := os.WriteFile(w.imgSigners, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if calls := cosignCalls(t); len(calls) != 0 {
		t.Errorf("con N9 cosign non va chiamato: %v", calls)
	}
}

// Con un registry mirror l'immagine si verifica sul mirror (nome senza host,
// --allow-insecure-registry solo se http) e senza firma lì è rifiutata.
func TestV5MirrorImmaginiVerificate(t *testing.T) {
	w := newWorld(t)
	w.o.V5.RegistryMirror = "http://registry.corp:5000"
	onMirror := "registry.corp:5000/fathormb/gitstack-core:sha-" + newSHA
	if err := os.WriteFile(w.imgSigners, []byte(onMirror+" "+validIdentity+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Il nome nel chart ha un altro host: conta il percorso.
	w.helm.images = "image: \"ghcr.io/fathormb/gitstack-core:sha-" + newSHA + "\"\n"
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	found := false
	for _, c := range cosignCalls(t) {
		if strings.HasPrefix(c, "verify ") && strings.Contains(c, onMirror) {
			found = true
			if !strings.Contains(c, "--allow-insecure-registry") {
				t.Errorf("mirror http senza --allow-insecure-registry: %s", c)
			}
		}
	}
	if !found {
		t.Errorf("immagine non verificata sul mirror: %v", cosignCalls(t))
	}
}

func TestV5MirrorImmaginiNonFirmateRifiutate(t *testing.T) {
	w := newWorld(t)
	w.o.V5.RegistryMirror = "https://registry.corp:5000"
	// Le firme stanno solo sul registry originale: sul mirror non ci sono.
	w.helm.images = "image: \"ghcr.io/fathormb/gitstack-core:sha-" + newSHA + "\"\n"
	runRefused(t, w, "registry.corp:5000/fathormb/gitstack-core", "no signatures found")
}

func TestV5MirroredImage(t *testing.T) {
	for _, c := range []struct {
		mirror, in, want string
		insecure         bool
	}{
		{"", "ghcr.io/o/gitstack-core:t", "ghcr.io/o/gitstack-core:t", false},
		{"https://r.corp:5000/", "ghcr.io/o/gitstack-core:t", "r.corp:5000/o/gitstack-core:t", false},
		{"http://r.corp:5000", "ghcr.io/o/gitstack-core@sha256:ab", "r.corp:5000/o/gitstack-core@sha256:ab", true},
	} {
		got, ins := V5Config{RegistryMirror: c.mirror}.mirroredImage(c.in)
		if got != c.want || ins != c.insecure {
			t.Errorf("%q con %q: %q,%v atteso %q,%v", c.in, c.mirror, got, ins, c.want, c.insecure)
		}
	}
}
