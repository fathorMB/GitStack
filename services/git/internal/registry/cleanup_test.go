package registry

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// GIT-325 (RG2): pulizia, tag protetti e quote. Le impostazioni arrivano da un finto core e
// l'orologio del registry è finto: i file nuovi hanno l'ora vera, il registry «vede» il tempo
// avanzare con clock.advance.

type settingsBackend struct {
	*fakeBackend
	mu sync.Mutex
	st map[string]Settings
}

func (s *settingsBackend) PackageSettings(_ context.Context, repoID string) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.st[repoID]
	if !ok {
		return Settings{}, ErrSettingsNotFound
	}
	if st.RepoID == "" {
		st.RepoID = repoID
	}
	return st, nil
}

func (s *settingsBackend) set(repoID string, f func(*Settings)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st[repoID]
	f(&st)
	s.st[repoID] = st
}

type fakeClock struct {
	mu   sync.Mutex
	base time.Time
	off  time.Duration
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.base.Add(c.off)
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.off += d
}

const day = 24 * time.Hour

type cleanEnv struct {
	*env
	sb    *settingsBackend
	clock *fakeClock
}

func newCleanEnv(t *testing.T, mod func(*Config)) *cleanEnv {
	t.Helper()
	b := newBackend()
	sb := &settingsBackend{fakeBackend: b, st: map[string]Settings{
		idApp:    {Owner: "alice", Name: "app", DeleteUntaggedAfterDays: 7, OwnerRepoIDs: []string{idApp, idSecret}},
		idSecret: {Owner: "alice", Name: "secret", DeleteUntaggedAfterDays: 7, OwnerRepoIDs: []string{idApp, idSecret}},
		idOther:  {Owner: "bob", Name: "other", DeleteUntaggedAfterDays: 7, OwnerRepoIDs: []string{idOther}},
	}}
	clock := &fakeClock{base: time.Now()}
	cfg := Config{Dir: t.TempDir(), Secret: "segreto", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: clock.now, SettingsTTL: -1, UsageTTL: -1}
	if runtime.GOOS == "windows" {
		cfg.Dir, cfg.testInMemory = "", true
	}
	if mod != nil {
		mod(&cfg)
	}
	h, err := New(cfg, sb)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &cleanEnv{env: &env{t: t, b: b, h: h, srv: srv, dir: cfg.Dir}, sb: sb, clock: clock}
}

func (e *cleanEnv) cleanup() CleanupReport {
	e.t.Helper()
	rep, err := e.h.Cleanup(context.Background())
	if err != nil {
		e.t.Fatalf("Cleanup: %v", err)
	}
	return rep
}

func (e *cleanEnv) tags(path string) []string {
	e.t.Helper()
	tags, err := remote.List(e.ref(path).Context(), basic("gst_read"))
	if err != nil {
		if strings.Contains(err.Error(), "NAME_UNKNOWN") {
			return nil
		}
		e.t.Fatal(err)
	}
	slices.Sort(tags)
	return tags
}

func (e *cleanEnv) manifestExists(path string, d v1.Hash) bool {
	e.t.Helper()
	_, err := remote.Head(e.ref(path+"@"+d.String()), basic("gst_read"))
	return err == nil
}

// pushAt pubblica un'immagine nuova e lascia passare un istante, così l'ordine dei file è netto.
func (e *cleanEnv) pushAt(path string) v1.Image {
	e.t.Helper()
	img := e.mustPush(path, "gst_write")
	time.Sleep(15 * time.Millisecond)
	return img
}

func digestOf(t *testing.T, img v1.Image) v1.Hash {
	t.Helper()
	d, err := img.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestCleanup_SenzaTagDopo7GiorniConTagMai(t *testing.T) {
	e := newCleanEnv(t, nil)
	old := e.pushAt("alice/app:latest")
	tagged := e.pushAt("alice/app:latest") // latest ora punta qui: «old» resta senza tag
	dOld, dTagged := digestOf(t, old), digestOf(t, tagged)

	e.clock.advance(6 * day)
	if rep := e.cleanup(); len(rep.DeletedImages)+len(rep.DeletedTags) != 0 {
		t.Fatalf("a 6 giorni non si elimina niente: %+v", rep)
	}
	if !e.manifestExists("alice/app", dOld) {
		t.Fatal("l'immagine senza tag è sparita prima di 7 giorni")
	}

	e.clock.advance(2 * day) // 8 giorni
	rep := e.cleanup()
	if len(rep.DeletedImages) != 1 || !strings.HasSuffix(rep.DeletedImages[0], dOld.String()) || len(rep.DeletedTags) != 0 {
		t.Fatalf("a 8 giorni va eliminata solo l'immagine senza tag: %+v", rep)
	}
	if e.manifestExists("alice/app", dOld) {
		t.Fatal("l'immagine senza tag è ancora lì")
	}
	// Con tag: mai in automatico, nemmeno dopo un anno.
	e.clock.advance(400 * day)
	if rep := e.cleanup(); len(rep.DeletedImages)+len(rep.DeletedTags) != 0 {
		t.Fatalf("un'immagine con tag non si elimina da sola: %+v", rep)
	}
	if !e.manifestExists("alice/app", dTagged) {
		t.Fatal("l'immagine con tag è sparita")
	}
}

func TestCleanup_ConservaFirmaESBOMFincheEsisteImmagine(t *testing.T) {
	e := newCleanEnv(t, func(c *Config) { c.SigningKeyFile = filepath.Join(t.TempDir(), "cosign.key") })
	img := e.mustPush("alice/app:job", "gsj_rw")
	d, err := img.Digest()
	if err != nil {
		t.Fatal(err)
	}
	prefix := strings.Replace(d.String(), ":", "-", 1)
	e.clock.advance(8 * day)
	if rep := e.cleanup(); len(rep.DeletedImages) != 0 || len(rep.DeletedTags) != 0 {
		t.Fatalf("la pulizia ha rimosso artefatti della immagine ancora taggata: %+v", rep)
	}
	if got := e.tags("alice/app"); !slices.Equal(got, []string{"job", prefix + ".sbom", prefix + ".sig"}) {
		t.Fatalf("firma/SBOM mancanti dopo pulizia: %v", got)
	}
}

func TestCleanup_GiorniPerRepoEdIndiceNonOrfano(t *testing.T) {
	e := newCleanEnv(t, nil)
	e.sb.set(idApp, func(s *Settings) { s.DeleteUntaggedAfterDays = 2 })
	old := e.pushAt("alice/app:x")
	e.pushAt("alice/app:x")
	e.clock.advance(3 * day)
	// alice/secret ha ancora 7 giorni e non ha niente da pulire; alice/app a 2 giorni sì.
	rep := e.cleanup()
	if len(rep.DeletedImages) != 1 || !strings.HasSuffix(rep.DeletedImages[0], digestOf(t, old).String()) {
		t.Fatalf("con deleteUntaggedAfterDays=2: %+v", rep)
	}
}

func TestCleanup_UltimeNEShaPiuVecchi(t *testing.T) {
	e := newCleanEnv(t, nil)
	e.sb.set(idApp, func(s *Settings) { s.KeepLastTags = 2; s.ProtectedTagPatterns = []string{"v*"} })
	for _, tag := range []string{"v1", "a", "b", "c", "d"} {
		e.pushAt("alice/app:" + tag)
	}
	e.cleanup()
	// Si tengono gli ultimi 2 non protetti (c, d); il protetto v1 resta sempre e non conta.
	if got := e.tags("alice/app"); !slices.Equal(got, []string{"c", "d", "v1"}) {
		t.Fatalf("tag dopo keepLastTags=2: %v", got)
	}
	// Le immagini dei tag eliminati sono ora senza tag: vanno via dopo 7 giorni.
	e.clock.advance(8 * day)
	e.cleanup()
	if got := e.tags("alice/app"); !slices.Equal(got, []string{"c", "d", "v1"}) {
		t.Fatalf("tag dopo 8 giorni: %v", got)
	}

	e2 := newCleanEnv(t, nil)
	e2.sb.set(idApp, func(s *Settings) { s.DeleteShaTagsAfterDays = 5; s.ProtectedTagPatterns = []string{"sha-keep*"} })
	for _, tag := range []string{"sha-aaa", "sha-keep1", "latest", "v1"} {
		e2.pushAt("alice/app:" + tag)
	}
	e2.clock.advance(4 * day)
	e2.cleanup()
	if got := e2.tags("alice/app"); len(got) != 4 {
		t.Fatalf("a 4 giorni (X=5) restano tutti: %v", got)
	}
	e2.clock.advance(2 * day) // 6 giorni
	e2.cleanup()
	// Solo sha-aaa: sha-keep1 è protetto, latest e v1 non sono sha-*.
	if got := e2.tags("alice/app"); !slices.Equal(got, []string{"latest", "sha-keep1", "v1"}) {
		t.Fatalf("tag dopo deleteShaTagsAfterDays=5: %v", got)
	}
}

func TestCleanup_SaltaArchiviatiESconosciutiEUsaLaGrazia(t *testing.T) {
	e := newCleanEnv(t, func(c *Config) { c.BlobGrace = 100 * day })
	e.pushAt("alice/app:x")
	old := e.pushAt("alice/app:x")
	_ = old
	e.mustPush("bob/other:y", "gst_admin")
	e.mustPush("bob/other:y", "gst_admin")
	e.sb.set(idApp, func(s *Settings) { s.Archived = true })
	e.sb.mu.Lock()
	delete(e.sb.st, idOther) // core non lo conosce più
	e.sb.mu.Unlock()
	e.clock.advance(30 * day)
	rep := e.cleanup()
	if len(rep.DeletedImages) != 0 || rep.Skipped < 2 {
		t.Fatalf("archiviato e sconosciuto non si toccano: %+v", rep)
	}
	// Repo normale: l'immagine va via ma i blob restano entro la tolleranza (100 giorni).
	e.sb.set(idApp, func(s *Settings) { s.Archived = false })
	before, _ := e.h.usageOf(context.Background(), []string{idApp})
	rep = e.cleanup()
	if len(rep.DeletedImages) != 1 {
		t.Fatalf("immagine senza tag: %+v", rep)
	}
	e.clock.advance(100 * day)
	e.cleanup()
	after, _ := e.h.usageOf(context.Background(), []string{idApp})
	if after >= before {
		t.Fatalf("i blob orfani vanno raccolti oltre la tolleranza: prima %d, dopo %d", before, after)
	}
}

func TestTagProtetti_NonSiSovrascrivonoNeSiEliminano(t *testing.T) {
	e := newCleanEnv(t, nil)
	e.sb.set(idApp, func(s *Settings) { s.ProtectedTagPatterns = []string{"v*"} })
	first := e.mustPush("alice/app:v1", "gst_write")
	// Sovrascrivere un tag protetto è negato, anche con il ruolo admin.
	_, err := e.push("alice/app:v1", "gst_write")
	wantErr(t, "sovrascrittura di v1", err, 403)
	_, err = e.push("alice/app:v1", "gst_admin")
	wantErr(t, "sovrascrittura di v1 (admin)", err, 403)
	if d, err := e.pull("alice/app:v1", "gst_read"); err != nil || d != digestOf(t, first) {
		t.Fatalf("v1 deve puntare ancora alla prima immagine: %v %v", d, err)
	}
	// Un tag protetto nuovo si pubblica; un tag non protetto si sovrascrive.
	e.mustPush("alice/app:v2", "gst_write")
	e.mustPush("alice/app:dev", "gst_write")
	e.mustPush("alice/app:dev", "gst_write")
	// Eliminare l'immagine che ha un tag protetto è negato; dopo che il modello non vale più, passa.
	wantErr(t, "delete di v1", remote.Delete(e.ref("alice/app@"+digestOf(t, first).String()), basic("gst_admin")), 403)
	if !e.manifestExists("alice/app", digestOf(t, first)) {
		t.Fatal("l'immagine con il tag protetto è sparita")
	}
	// DELETE per tag (distribution lo accetta e fa Untag): su un tag protetto è 403 e il tag resta.
	if st := e.status("DELETE", "/v2/alice/app/manifests/v1", "gst_admin"); st != 403 {
		t.Fatalf("DELETE per tag protetto: %d, voluto 403", st)
	}
	if d, err := e.pull("alice/app:v1", "gst_read"); err != nil || d != digestOf(t, first) {
		t.Fatalf("v1 dopo la DELETE per tag: %v %v", d, err)
	}
	// Su un tag non protetto la DELETE per tag passa (202 di distribution) e il tag sparisce.
	if st := e.status("DELETE", "/v2/alice/app/manifests/dev", "gst_admin"); st != 202 {
		t.Fatalf("DELETE per tag non protetto: %d, voluto 202", st)
	}
	if got := e.tags("alice/app"); slices.Contains(got, "dev") || !slices.Contains(got, "v1") {
		t.Fatalf("tag dopo la DELETE di dev: %v", got)
	}
	e.sb.set(idApp, func(s *Settings) { s.ProtectedTagPatterns = nil })
	if err := remote.Delete(e.ref("alice/app@"+digestOf(t, first).String()), basic("gst_admin")); err != nil {
		t.Fatalf("delete senza protezione: %v", err)
	}
}

func TestQuota_PushOltreQuotaRifiutatoEAvvisoOltre80(t *testing.T) {
	e := newCleanEnv(t, nil)
	// Misura quanto pesa un'immagine di prova, poi fissa la quota a poco più di un'immagine e mezza.
	e.mustPush("alice/app:uno", "gst_write")
	one, err := e.h.usageOf(context.Background(), []string{idApp})
	if err != nil || one < 4000 {
		t.Fatalf("peso dell'immagine: %d %v", one, err)
	}
	e.h.quota.Store(one * 5 / 4)

	// Oltre l'80% della quota: il push continua ma arriva l'avviso.
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/v2/alice/app/blobs/uploads/", nil)
	req.SetBasicAuth("docker", "gst_write")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted || !strings.Contains(resp.Header.Get("Warning"), "quota") {
		t.Fatalf("avvisa oltre l'80%%: %d Warning=%q", resp.StatusCode, resp.Header.Get("Warning"))
	}
	// Sotto l'80% niente avviso.
	e.h.quota.Store(one * 10)
	req2, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/v2/alice/app/blobs/uploads/", nil)
	req2.SetBasicAuth("docker", "gst_write")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp2.Body.Close()
	if resp2.Header.Get("Warning") != "" {
		t.Fatalf("sotto l'80%% non c'è avviso: %q", resp2.Header.Get("Warning"))
	}

	// Quota esaurita: il secondo push, anche su un altro repo dello stesso owner, è rifiutato.
	e.h.quota.Store(one)
	_, err = e.push("alice/app:due", "gst_write")
	wantErr(t, "push oltre quota", err, 403)
	_, err = e.push("alice/secret:x", "gst_admin")
	wantErr(t, "push oltre quota su un altro repo dello stesso owner", err, 403)
	// La quota è per owner: bob non ne risente. Scaricare e eliminare restano possibili.
	e.h.quota.Store(one * 3)
	e.mustPush("bob/other:x", "gst_admin")
	if _, err := e.pull("alice/app:uno", "gst_read"); err != nil {
		t.Fatalf("pull oltre quota: %v", err)
	}
	// Eliminare libera spazio e il push torna possibile.
	d := digestOf(t, mustImg(t, e, "alice/app:uno"))
	if err := remote.Delete(e.ref("alice/app@"+d.String()), basic("gst_admin")); err != nil {
		t.Fatal(err)
	}
	e.clock.advance(2 * day)
	e.cleanup() // raccoglie i blob rimasti senza immagine (oltre la tolleranza)
	e.h.quota.Store(one * 2)
	e.mustPush("alice/app:tre", "gst_write")
}

func mustImg(t *testing.T, e *cleanEnv, path string) v1.Image {
	t.Helper()
	img, err := remote.Image(e.ref(path), basic("gst_read"))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestQuota_SenzaImpostazioniDiCoreNienteQuotaNeProtezioni(t *testing.T) {
	// Un backend che non dà le impostazioni (SettingsSource assente) non applica regole.
	e := newEnvWith(t, func(c *Config) { c.QuotaBytes = 1 })
	e.mustPush("alice/app:v1", "gst_write")
	e.mustPush("alice/app:v1", "gst_write")
}
