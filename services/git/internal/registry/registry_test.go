package registry

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	_ "github.com/distribution/distribution/v3/registry/storage/driver/inmemory"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"

	"github.com/fathorMB/GitStack/services/git/internal/access"
	"github.com/fathorMB/GitStack/services/git/internal/trust"
)

const (
	idApp    = "11111111-1111-4111-8111-111111111111"
	idSecret = "22222222-2222-4222-8222-222222222222"
	idOther  = "33333333-3333-4333-8333-333333333333"
)

var roleRank = map[string]int{"read": 1, "write": 2, "admin": 3}

type fakeRepo struct {
	id, owner, name   string
	archived, deleted bool
	// roles: utente → ruolo. Chi non c'è non legge (core risponde 404).
	roles map[string]string
}

type fakeJob struct {
	repoID   string
	packages string
}

// fakeBackend è identity, core e ci-gateway.
type fakeBackend struct {
	mu     sync.Mutex
	tokens map[string]access.Principal
	repos  map[string]*fakeRepo
	jobs   map[string]fakeJob
	calls  int
}

func newBackend() *fakeBackend {
	b := &fakeBackend{tokens: map[string]access.Principal{}, repos: map[string]*fakeRepo{}, jobs: map[string]fakeJob{}}
	b.repos["alice/app"] = &fakeRepo{id: idApp, owner: "alice", name: "app", roles: map[string]string{
		"u-admin": "admin", "u-write": "write", "u-read": "read",
	}}
	b.repos["alice/secret"] = &fakeRepo{id: idSecret, owner: "alice", name: "secret", roles: map[string]string{"u-admin": "admin"}}
	b.repos["bob/other"] = &fakeRepo{id: idOther, owner: "bob", name: "other", roles: map[string]string{"u-admin": "admin"}}
	add := func(tok, user string, scopes ...string) {
		b.tokens[tok] = access.Principal{UserID: user, Username: user, Scopes: scopes}
	}
	add("gst_admin", "u-admin", "admin:registry")
	add("gst_write", "u-write", "write:registry")
	add("gst_read", "u-read", "read:registry")
	// Ruolo admin ma scope solo di lettura: lo scope limita il ruolo.
	add("gst_admin_ro", "u-admin", "read:registry")
	// Ruolo write con scope admin: il ruolo limita lo scope.
	add("gst_write_scopeadmin", "u-write", "admin:registry")
	// Scope di codice, non del registry.
	add("gst_code", "u-admin", "write:resource")
	b.jobs["gsj_rw"] = fakeJob{repoID: idApp, packages: "write"}
	b.jobs["gsj_ro"] = fakeJob{repoID: idApp, packages: "read"}
	b.jobs["gsj_none"] = fakeJob{repoID: idApp, packages: "none"}
	return b
}

func (b *fakeBackend) find(owner, repo string) *fakeRepo {
	return b.repos[strings.ToLower(owner)+"/"+strings.ToLower(repo)]
}

func (b *fakeBackend) VerifyToken(_ context.Context, token string) (access.Principal, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.tokens[token]
	return p, ok, nil
}

func (b *fakeBackend) HasRole(_ context.Context, userID, resourceID, role string) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, r := range b.repos {
		if r.id == resourceID {
			return roleRank[r.roles[userID]] >= roleRank[role], nil
		}
	}
	return false, nil
}

func (b *fakeBackend) ResolveRepo(_ context.Context, caller trust.Identity, owner, repo string) (access.RepoRef, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	r := b.find(owner, repo)
	if r == nil || r.deleted || r.roles[caller.UserID] == "" {
		return access.RepoRef{}, access.ErrNotFound
	}
	return access.RepoRef{ID: r.id, Owner: r.owner, Name: r.name, Archived: r.archived}, nil
}

func (b *fakeBackend) JobAccess(_ context.Context, token, owner, repo string) (access.JobAccess, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	j, ok := b.jobs[token]
	if !ok {
		return access.JobAccess{}, access.ErrInvalidJobToken
	}
	r := b.find(owner, repo)
	if r == nil || r.deleted || r.id != j.repoID {
		return access.JobAccess{}, access.ErrNotFound
	}
	return access.JobAccess{RepoID: r.id, Owner: r.owner, Name: r.name, Archived: r.archived, Packages: j.packages}, nil
}

func (b *fakeBackend) set(key string, f func(r *fakeRepo)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	f(b.repos[key])
}

type env struct {
	t   *testing.T
	b   *fakeBackend
	h   *Handler
	srv *httptest.Server
	dir string
}

func newEnv(t *testing.T) *env { return newEnvWith(t, nil) }

func newEnvWith(t *testing.T, mod func(*Config)) *env {
	t.Helper()
	dir := t.TempDir()
	cfg := Config{Dir: dir, Secret: "segreto", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if runtime.GOOS == "windows" {
		// Il driver su disco di distribution non gira su Windows (rinomina di file aperti):
		// qui si prova tutto il resto in memoria, il disco in Linux (CI e Docker).
		cfg.Dir, cfg.testInMemory = "", true
	}
	if mod != nil {
		mod(&cfg)
	}
	b := newBackend()
	h, err := New(cfg, b)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &env{t: t, b: b, h: h, srv: srv, dir: dir}
}

func (e *env) host() string { return strings.TrimPrefix(e.srv.URL, "http://") }

func (e *env) ref(path string) name.Reference {
	e.t.Helper()
	r, err := name.ParseReference(e.host()+"/"+path, name.Insecure)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func basic(tok string) remote.Option {
	return remote.WithAuth(&authn.Basic{Username: "docker", Password: tok})
}

func (e *env) push(path, tok string) (v1.Image, error) {
	e.t.Helper()
	img, err := random.Image(2048, 2)
	if err != nil {
		e.t.Fatal(err)
	}
	return img, remote.Write(e.ref(path), img, basic(tok))
}

func (e *env) mustPush(path, tok string) v1.Image {
	e.t.Helper()
	img, err := e.push(path, tok)
	if err != nil {
		e.t.Fatalf("push %s: %v", path, err)
	}
	return img
}

func (e *env) pull(path, tok string) (v1.Hash, error) {
	e.t.Helper()
	img, err := remote.Image(e.ref(path), basic(tok))
	if err != nil {
		return v1.Hash{}, err
	}
	// Scarica davvero i livelli, non solo il manifest.
	layers, err := img.Layers()
	if err != nil {
		return v1.Hash{}, err
	}
	for _, l := range layers {
		rc, err := l.Compressed()
		if err != nil {
			return v1.Hash{}, err
		}
		if _, err := io.Copy(io.Discard, rc); err != nil {
			return v1.Hash{}, err
		}
		_ = rc.Close()
	}
	return img.Digest()
}

// status esegue una richiesta e dà il codice HTTP.
func (e *env) status(method, path, tok string) int {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, nil)
	if tok != "" {
		req.SetBasicAuth("docker", tok)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	return res.StatusCode
}

func wantErr(t *testing.T, what string, err error, code int) {
	t.Helper()
	var te *transport.Error
	if err == nil {
		t.Errorf("%s: doveva fallire con %d", what, code)
		return
	}
	if !errors.As(err, &te) {
		t.Errorf("%s: errore %v (atteso HTTP %d)", what, err, code)
		return
	}
	if te.StatusCode != code {
		t.Errorf("%s: HTTP %d, atteso %d (%v)", what, te.StatusCode, code, err)
	}
}

func TestSenzaCredenziali_Mai(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/v2/", "/v2", "/v2/alice/app/manifests/latest", "/v2/alice/app/tags/list", "/v2/_catalog", "/v2/nessuno/niente/blobs/uploads/"} {
		for _, m := range []string{"GET", "HEAD", "POST", "PUT", "DELETE"} {
			if st := e.status(m, p, ""); st != 401 {
				t.Errorf("%s %s senza credenziali = %d, atteso 401", m, p, st)
			}
		}
	}
	for _, p := range []string{"/v2/", "/v2/alice/app/manifests/latest"} {
		if st := e.status("GET", p, "gst_inventato"); st != 401 {
			t.Errorf("token sconosciuto su %s = %d", p, st)
		}
		if st := e.status("GET", p, "gsj_inventato"); st != 401 && p == "/v2/" {
			t.Errorf("token del job sconosciuto su /v2/ = %d", st)
		}
	}
	// Con credenziali valide /v2/ dice solo che il registry c'è.
	if st := e.status("GET", "/v2/", "gst_read"); st != 200 {
		t.Errorf("/v2/ con token valido = %d", st)
	}
	req, _ := http.NewRequest("GET", e.srv.URL+"/v2/", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if !strings.HasPrefix(res.Header.Get("WWW-Authenticate"), "Basic realm=") {
		t.Errorf("la sfida Basic manca: %q", res.Header.Get("WWW-Authenticate"))
	}
}

func TestPermessi_ReadWriteAdmin(t *testing.T) {
	e := newEnv(t)

	// write pubblica; read scarica; chi non ha scope o ruolo non fa nulla.
	img := e.mustPush("alice/app:v1", "gst_write")
	want, _ := img.Digest()
	if got, err := e.pull("alice/app:v1", "gst_read"); err != nil || got != want {
		t.Fatalf("pull con read: %v %v (atteso %s)", got, err, want)
	}
	if got, err := e.pull("alice/app:v1", "gst_write"); err != nil || got != want {
		t.Fatalf("pull con write (implica read): %v %v", got, err)
	}

	_, err := e.push("alice/app:v2", "gst_read")
	wantErr(t, "push con scope read", err, 403)
	_, err = e.push("alice/app:v2", "gst_code")
	wantErr(t, "push con scope resource", err, 403)
	_, err = e.pull("alice/app:v1", "gst_code")
	wantErr(t, "pull con scope resource", err, 403)

	// Ruolo read ma scope admin:registry: il ruolo limita lo scope.
	e.b.tokens["gst_read_scopeadmin"] = access.Principal{UserID: "u-read", Username: "u-read", Scopes: []string{"admin:registry"}}
	_, err = e.push("alice/app:v2", "gst_read_scopeadmin")
	wantErr(t, "push con ruolo read", err, 403)
	// Ruolo admin ma scope di lettura: lo scope limita il ruolo.
	_, err = e.push("alice/app:v2", "gst_admin_ro")
	wantErr(t, "push con scope read e ruolo admin", err, 403)

	// Elimina: admin sì, write no, né per scope né per ruolo.
	ref := e.ref("alice/app@" + want.String())
	wantErr(t, "delete con write", remote.Delete(ref, basic("gst_write")), 403)
	wantErr(t, "delete con scope admin e ruolo write", remote.Delete(ref, basic("gst_write_scopeadmin")), 403)
	wantErr(t, "delete con ruolo admin e scope read", remote.Delete(ref, basic("gst_admin_ro")), 403)
	if err := remote.Delete(ref, basic("gst_admin")); err != nil {
		t.Fatalf("delete con admin: %v", err)
	}
	_, err = e.pull("alice/app:v1", "gst_read")
	wantErr(t, "pull dopo delete", err, 404)

	// Immagini con un nome sotto il repo.
	e.mustPush("alice/app/web:1.0", "gst_write")
	if _, err := e.pull("alice/app/web:1.0", "gst_read"); err != nil {
		t.Errorf("pull dell'immagine con nome: %v", err)
	}
	// Maiuscole nell'indirizzo (R11) riportano allo stesso repo.
	if st := e.status("GET", "/v2/Alice/APP/web/tags/list", "gst_read"); st != 200 {
		t.Errorf("maiuscole nell'indirizzo (R11) = %d, atteso lo stesso repo", st)
	}
}

func TestVisibilita_RepoNonLeggibileOInesistente_404(t *testing.T) {
	e := newEnv(t)
	e.mustPush("alice/secret:v1", "gst_admin")
	// u-read non ha alcun ruolo su alice/secret: come se non esistesse.
	_, err := e.pull("alice/secret:v1", "gst_read")
	wantErr(t, "repo privato non leggibile", err, 404)
	_, err = e.pull("alice/niente:v1", "gst_read")
	wantErr(t, "repo inesistente", err, 404)
	_, err = e.push("alice/secret:v2", "gst_write")
	wantErr(t, "push su repo non leggibile", err, 404)
	if st := e.status("GET", "/v2/alice/secret/tags/list", "gst_read"); st != 404 {
		t.Errorf("tags/list di un repo non leggibile = %d", st)
	}
	// Un solo nome senza repo non è un indirizzo del registry.
	if st := e.status("GET", "/v2/alice/manifests/latest", "gst_read"); st == 200 {
		t.Errorf("owner senza repo = %d", st)
	}
	if st := e.status("GET", "/v2/_catalog", "gst_admin"); st == 200 {
		t.Errorf("il catalogo non deve esistere: %d", st)
	}
}

func TestTokenDelJob(t *testing.T) {
	e := newEnv(t)
	e.mustPush("alice/app:base", "gst_write")

	if _, err := e.push("alice/app:ci", "gsj_rw"); err != nil {
		t.Fatalf("push con packages write: %v", err)
	}
	if _, err := e.pull("alice/app:ci", "gsj_rw"); err != nil {
		t.Fatalf("pull con packages write: %v", err)
	}
	if _, err := e.pull("alice/app:base", "gsj_ro"); err != nil {
		t.Fatalf("pull con packages read: %v", err)
	}
	_, err := e.push("alice/app:x", "gsj_ro")
	wantErr(t, "push con packages read", err, 403)
	_, err = e.pull("alice/app:base", "gsj_none")
	wantErr(t, "pull con packages none", err, 403)
	// Il token del job non elimina, nemmeno con write.
	img, _ := e.pull("alice/app:ci", "gst_read")
	wantErr(t, "delete con il token del job", remote.Delete(e.ref("alice/app@"+img.String()), basic("gsj_rw")), 403)
	// Solo il repo del job.
	_, err = e.push("bob/other:x", "gsj_rw")
	wantErr(t, "push su un altro repo", err, 404)
	_, err = e.pull("alice/secret:x", "gsj_rw")
	wantErr(t, "pull da un altro repo", err, 404)
	// Token del job non valido (scaduto, revocato, sconosciuto) sulle rotte dei repo.
	_, err = e.pull("alice/app:base", "gsj_scaduto")
	wantErr(t, "token del job sconosciuto", err, 401)

	// Archiviato: il job scarica, non pubblica.
	e.b.set("alice/app", func(r *fakeRepo) { r.archived = true })
	if _, err := e.pull("alice/app:base", "gsj_rw"); err != nil {
		t.Errorf("pull da repo archiviato con il token del job: %v", err)
	}
	_, err = e.push("alice/app:late", "gsj_rw")
	wantErr(t, "push del job su repo archiviato", err, 403)
}

func TestArchiviato_PullSi_PushNo(t *testing.T) {
	e := newEnv(t)
	e.mustPush("alice/app:v1", "gst_admin")
	e.b.set("alice/app", func(r *fakeRepo) { r.archived = true })

	for _, tok := range []string{"gst_read", "gst_write", "gst_admin"} {
		if _, err := e.pull("alice/app:v1", tok); err != nil {
			t.Errorf("pull da repo archiviato con %s: %v", tok, err)
		}
	}
	for _, tok := range []string{"gst_write", "gst_admin"} {
		_, err := e.push("alice/app:v2", tok)
		wantErr(t, "push su repo archiviato con "+tok, err, 403)
	}
	d, _ := e.pull("alice/app:v1", "gst_read")
	wantErr(t, "delete su repo archiviato", remote.Delete(e.ref("alice/app@"+d.String()), basic("gst_admin")), 403)

	// Riattivato: si pubblica di nuovo.
	e.b.set("alice/app", func(r *fakeRepo) { r.archived = false })
	e.mustPush("alice/app:v2", "gst_write")
}

func TestEliminatoERipristinato_LeImmaginiTornano(t *testing.T) {
	e := newEnv(t)
	img := e.mustPush("alice/app/api:1", "gst_write")
	want, _ := img.Digest()

	e.b.set("alice/app", func(r *fakeRepo) { r.deleted = true })
	for _, tok := range []string{"gst_admin", "gst_read", "gsj_rw"} {
		_, err := e.pull("alice/app/api:1", tok)
		wantErr(t, "pull da repo eliminato con "+tok, err, 404)
	}
	_, err := e.push("alice/app/api:2", "gst_write")
	wantErr(t, "push su repo eliminato", err, 404)

	e.b.set("alice/app", func(r *fakeRepo) { r.deleted = false })
	got, err := e.pull("alice/app/api:1", "gst_read")
	if err != nil || got != want {
		t.Fatalf("dopo il ripristino: %v %v, attesa la stessa immagine %s", got, err, want)
	}
}

func TestCancellazioneDefinitiva_TogliLeImmagini(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("serve l'archiviazione su disco: in memoria Purge usa un'istanza separata")
	}
	e := newEnv(t)
	e.mustPush("alice/app:v1", "gst_write")
	e.mustPush("alice/secret:v1", "gst_admin")

	if err := e.h.Purge(context.Background(), idApp); err != nil {
		t.Fatal(err)
	}
	if err := e.h.Purge(context.Background(), idApp); err != nil {
		t.Errorf("purge ripetuta: %v", err)
	}
	// Anche con un repo con lo stesso nome (ricreato) non torna nulla.
	_, err := e.pull("alice/app:v1", "gst_read")
	wantErr(t, "pull dopo la cancellazione definitiva", err, 404)
	if _, err := e.pull("alice/secret:v1", "gst_admin"); err != nil {
		t.Errorf("l'altro repo non va toccato: %v", err)
	}
	if err := e.h.Purge(context.Background(), "../..//x"); err == nil {
		t.Error("un id non valido non deve arrivare al disco")
	}
}

// Lo stesso repo, rinominato, è lo stesso registry: le immagini stanno sotto l'id.
func TestRinomina_LeImmaginiSeguonoIlRepo(t *testing.T) {
	e := newEnv(t)
	img := e.mustPush("alice/app:v1", "gst_write")
	want, _ := img.Digest()
	e.b.mu.Lock()
	r := e.b.repos["alice/app"]
	delete(e.b.repos, "alice/app")
	r.name = "nuovo"
	e.b.repos["alice/nuovo"] = r
	e.b.mu.Unlock()
	if got, err := e.pull("alice/nuovo:v1", "gst_read"); err != nil || got != want {
		t.Fatalf("dopo la rinomina: %v %v", got, err)
	}
	_, err := e.pull("alice/app:v1", "gst_read")
	wantErr(t, "vecchio nome", err, 404)
}

func TestLocationEdIdNonTrapelano(t *testing.T) {
	e := newEnv(t)
	req, _ := http.NewRequest("POST", e.srv.URL+"/v2/alice/app/web/blobs/uploads/", nil)
	req.SetBasicAuth("x", "gst_write")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	loc := res.Header.Get("Location")
	if res.StatusCode != 202 || !strings.HasPrefix(loc, "/v2/alice/app/web/blobs/uploads/") || strings.Contains(loc, idApp) {
		t.Errorf("upload: %d Location=%q (atteso l'indirizzo owner/repo, mai l'id)", res.StatusCode, loc)
	}
	// Il seguito dell'upload sull'indirizzo restituito è autorizzato come il resto.
	if st := e.status("DELETE", loc, "gst_read"); st != 403 {
		t.Errorf("DELETE dell'upload con read = %d", st)
	}
	if st := e.status("GET", loc, ""); st != 401 {
		t.Errorf("upload senza credenziali = %d", st)
	}
	// L'id del repo non è un indirizzo valido.
	if st := e.status("GET", "/v2/"+idApp+"/manifests/latest", "gst_admin"); st == 200 {
		t.Errorf("l'id come owner = %d", st)
	}
}

func TestMontaggioDiBlob_SoloDaRepoLeggibili(t *testing.T) {
	e := newEnv(t)
	img := e.mustPush("alice/secret:v1", "gst_admin")
	layers, _ := img.Layers()
	d, _ := layers[0].Digest()

	mount := func(tok, from string) int {
		req, _ := http.NewRequest("POST", e.srv.URL+"/v2/alice/app/blobs/uploads/?mount="+d.String()+"&from="+from, nil)
		req.SetBasicAuth("x", tok)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	}
	// u-write scrive in alice/app ma non legge alice/secret: nessun montaggio, upload normale (202).
	if st := mount("gst_write", "alice/secret"); st != 202 {
		t.Errorf("montaggio da repo non leggibile = %d, atteso 202 (upload normale)", st)
	}
	// u-admin legge entrambi: montato (201).
	if st := mount("gst_admin", "alice/secret"); st != 201 {
		t.Errorf("montaggio da repo leggibile = %d, atteso 201", st)
	}
	// Con il blob di un altro repo che non esiste sotto un nome fuori dal registry.
	if st := mount("gst_admin", "bob"); st != 202 {
		t.Errorf("montaggio con from non valido = %d", st)
	}
}

func TestDischi_ErroriDiConfigurazione(t *testing.T) {
	b := newBackend()
	if _, err := New(Config{}, b); err == nil {
		t.Error("senza cartella né S3 doveva fallire")
	}
	if _, err := New(Config{S3: &S3{}}, b); err == nil {
		t.Error("S3 senza bucket doveva fallire")
	}
	if _, err := New(Config{Dir: t.TempDir()}, nil); err == nil {
		t.Error("senza backend doveva fallire")
	}
	if runtime.GOOS == "windows" {
		return
	}
	// La cartella del registry non è mai sotto un repo: i dati stanno dove diciamo.
	e := newEnv(t)
	e.mustPush("alice/app:v1", "gst_write")
	if _, err := os.Stat(filepath.Join(e.dir, "docker", "registry", "v2", "repositories", idApp)); err != nil {
		t.Errorf("le immagini devono stare sotto l'id del repo: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "docker", "registry", "v2", "repositories", "alice")); err == nil {
		t.Error("le immagini non devono stare sotto owner/nome")
	}
}
