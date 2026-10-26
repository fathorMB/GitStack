package backup

import (
	"archive/tar"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const (
	regRepoIn  = "11111111-1111-4111-8111-111111111111" // includeInBackup
	regRepoOut = "22222222-2222-4222-8222-222222222222" // default: fuori
	regShared  = "aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111"
	regOnlyIn  = "bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222"
	regOnlyOut = "cccc3333cccc3333cccc3333cccc3333cccc3333cccc3333cccc3333cccc3333"
)

// seedRegistry crea nel volume git-data un registry distribution con due repo: uno dei due
// blob in comune, uno solo suo ciascuno, più un upload in corso.
func seedRegistry(t *testing.T, git string) {
	t.Helper()
	v2 := filepath.Join(git, "registry", "docker", "registry", "v2")
	write := func(rel, c string) {
		p := filepath.Join(v2, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	blob := func(hex string) { write("blobs/sha256/"+hex[:2]+"/"+hex+"/data", "BLOB-"+hex[:4]) }
	for _, hex := range []string{regShared, regOnlyIn, regOnlyOut} {
		blob(hex)
	}
	write("repositories/"+regRepoIn+"/_layers/sha256/"+regShared+"/link", "sha256:"+regShared)
	write("repositories/"+regRepoIn+"/_layers/sha256/"+regOnlyIn+"/link", "sha256:"+regOnlyIn)
	write("repositories/"+regRepoIn+"/_manifests/tags/v1/current/link", "sha256:"+regOnlyIn)
	write("repositories/"+regRepoIn+"/_uploads/u1/data", "UPLOAD-IN-CORSO")
	write("repositories/"+regRepoOut+"/_layers/sha256/"+regShared+"/link", "sha256:"+regShared)
	write("repositories/"+regRepoOut+"/_layers/sha256/"+regOnlyOut+"/link", "sha256:"+regOnlyOut)
	write("repositories/"+regRepoOut+"/_manifests/tags/v1/current/link", "sha256:"+regOnlyOut)
}

func tarNames(t *testing.T, path, member string) []string {
	t.Helper()
	tr, closeFn, err := openArchive(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	for {
		h, err := tr.Next()
		if err != nil {
			t.Fatalf("%s non è nell'archivio: %v", member, err)
		}
		if h.Name != member {
			continue
		}
		inner := tar.NewReader(tr)
		var names []string
		for {
			ih, err := inner.Next()
			if err != nil {
				break
			}
			if !strings.HasSuffix(ih.Name, "/") {
				names = append(names, ih.Name)
			}
		}
		sort.Strings(names)
		return names
	}
}

func hasSuffixAny(names []string, suffix string) bool {
	for _, n := range names {
		if strings.HasSuffix(n, suffix) {
			return true
		}
	}
	return false
}

// RG2: le immagini sono escluse di default dal backup, incluse con includeInBackup; il restore
// riporta le sole immagini incluse (e i blob che referenziano).
func TestBackupImmaginiEscluseDiDefaultIncluseConIncludeInBackup(t *testing.T) {
	e := newEnv(t)
	seedRegistry(t, e.git)

	// Default: nessun repo con includeInBackup. Il git resta, il registry no.
	res, err := Backup(context.Background(), e.o)
	if err != nil {
		t.Fatal(err)
	}
	names := tarNames(t, res.Path, GitDataName)
	if !hasSuffixAny(names, "alice/repo.git/HEAD") {
		t.Fatalf("i repo git devono esserci: %v", names)
	}
	for _, n := range names {
		if strings.HasPrefix(n, "registry/") {
			t.Fatalf("di default le immagini sono fuori dal backup: %s", n)
		}
	}
	if len(res.Manifest.RegistryRepos) != 0 {
		t.Errorf("manifest: %v", res.Manifest.RegistryRepos)
	}

	// includeInBackup su un repo: le sue cartelle e i blob che referenzia, non gli altri né gli upload.
	e.f.registryRepos = regRepoIn + "\n"
	e.o.Now = nil // un secondo archivio con un nome diverso
	res, err = Backup(context.Background(), e.o)
	if err != nil {
		t.Fatal(err)
	}
	names = tarNames(t, res.Path, GitDataName)
	wantIn := []string{
		"registry/docker/registry/v2/repositories/" + regRepoIn + "/_layers/sha256/" + regShared + "/link",
		"registry/docker/registry/v2/repositories/" + regRepoIn + "/_layers/sha256/" + regOnlyIn + "/link",
		"registry/docker/registry/v2/repositories/" + regRepoIn + "/_manifests/tags/v1/current/link",
		"registry/docker/registry/v2/blobs/sha256/" + regShared[:2] + "/" + regShared + "/data",
		"registry/docker/registry/v2/blobs/sha256/" + regOnlyIn[:2] + "/" + regOnlyIn + "/data",
	}
	for _, w := range wantIn {
		if !hasSuffixAny(names, w) {
			t.Errorf("manca %s", w)
		}
	}
	for _, n := range names {
		if strings.Contains(n, regRepoOut) || strings.Contains(n, regOnlyOut) || strings.Contains(n, "_uploads") {
			t.Errorf("non doveva entrare: %s", n)
		}
	}
	if len(res.Manifest.RegistryRepos) != 1 || res.Manifest.RegistryRepos[0] != regRepoIn {
		t.Errorf("manifest: %v", res.Manifest.RegistryRepos)
	}

	// Restore su un'installazione pulita: tornano le immagini incluse, non le altre.
	r := newEnv(t)
	seedRegistry(t, r.git)
	r.o.DestDir = filepath.Join(t.TempDir(), "b")
	if err := Restore(context.Background(), r.o, res.Path); err != nil {
		t.Fatal(err)
	}
	v2 := filepath.Join(r.git, "registry", "docker", "registry", "v2")
	for p, c := range map[string]string{
		filepath.Join(v2, "blobs", "sha256", regOnlyIn[:2], regOnlyIn, "data"):                      "BLOB-bbbb",
		filepath.Join(v2, "blobs", "sha256", regShared[:2], regShared, "data"):                      "BLOB-aaaa",
		filepath.Join(v2, "repositories", regRepoIn, "_manifests", "tags", "v1", "current", "link"): "sha256:" + regOnlyIn,
	} {
		if b, err := os.ReadFile(p); err != nil || string(b) != c {
			t.Errorf("%s = %q, %v; atteso %q", p, b, err, c)
		}
	}
	for _, p := range []string{
		filepath.Join(v2, "repositories", regRepoOut),
		filepath.Join(v2, "blobs", "sha256", regOnlyOut[:2], regOnlyOut),
		filepath.Join(v2, "repositories", regRepoIn, "_uploads"),
	} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("dopo il restore non doveva esserci %s", p)
		}
	}
}

// Immagini delle app (H4): il registry con due repo e più tag per repo. Repo A (regRepoIn) con
// v1 (M1), v2 (indice I che punta al figlio M2) e v3 (M3, non in uso); repo B (regRepoOut) con
// v1 (M4) e v0 (M5, non in uso).
const (
	appM1, appM2, appM3, appI, appM4, appM5 = "1111111111111111111111111111111111111111111111111111111111111111",
		"2222222222222222222222222222222222222222222222222222222222222222",
		"3333333333333333333333333333333333333333333333333333333333333333",
		"4444444444444444444444444444444444444444444444444444444444444444",
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	appC1, appC2, appC3, appC4 = "5555555555555555555555555555555555555555555555555555555555555555",
		"6666666666666666666666666666666666666666666666666666666666666666",
		"7777777777777777777777777777777777777777777777777777777777777777",
		"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	appL1, appL2, appL3, appL4, appL5 = "8888888888888888888888888888888888888888888888888888888888888888",
		"9999999999999999999999999999999999999999999999999999999999999999",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		"0000000000000000000000000000000000000000000000000000000000000000"
)

// seedAppRegistry scrive i due repo di appRepos con i loro manifest (JSON veri, per la chiusura
// dei blob) e i tag. Ogni tag ha il suo link «index» di storia, che il backup non deve portare.
func seedAppRegistry(t *testing.T, git string) {
	t.Helper()
	v2 := filepath.Join(git, "registry", "docker", "registry", "v2")
	write := func(rel, c string) {
		p := filepath.Join(v2, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	blob := func(hex, content string) { write("blobs/sha256/"+hex[:2]+"/"+hex+"/data", content) }
	manifest := func(hex, body string) { blob(hex, body) }
	for _, h := range []string{appC1, appC2, appC3, appC4, appL1, appL2, appL3, appL4, appL5} {
		blob(h, "BLOB-"+h[:4])
	}
	manifest(appM1, `{"schemaVersion":2,"config":{"digest":"sha256:`+appC1+`"},"layers":[{"digest":"sha256:`+appL1+`"}]}`)
	manifest(appM2, `{"schemaVersion":2,"config":{"digest":"sha256:`+appC2+`"},"layers":[{"digest":"sha256:`+appL2+`"}]}`)
	manifest(appM3, `{"schemaVersion":2,"config":{"digest":"sha256:`+appC3+`"},"layers":[{"digest":"sha256:`+appL3+`"}]}`)
	manifest(appI, `{"schemaVersion":2,"manifests":[{"digest":"sha256:`+appM2+`"}]}`)
	manifest(appM4, `{"schemaVersion":2,"config":{"digest":"sha256:`+appC4+`"},"layers":[{"digest":"sha256:`+appL4+`"}]}`)
	manifest(appM5, `{"schemaVersion":2,"layers":[{"digest":"sha256:`+appL5+`"}]}`)

	repo := func(id string, tags map[string]string, revs []string, layers map[string]string) {
		root := "repositories/" + id
		for tag, dg := range tags {
			write(root+"/_manifests/tags/"+tag+"/current/link", "sha256:"+dg)
			write(root+"/_manifests/tags/"+tag+"/index/sha256/"+dg+"/link", "sha256:"+dg)
		}
		for _, r := range revs {
			write(root+"/_manifests/revisions/sha256/"+r+"/link", "sha256:"+r)
		}
		for _, l := range layers {
			write(root+"/_layers/sha256/"+l+"/link", "sha256:"+l)
		}
	}
	repo(regRepoIn,
		map[string]string{"v1": appM1, "v2": appI, "v3": appM3},
		[]string{appM1, appM2, appM3, appI},
		map[string]string{"c1": appC1, "l1": appL1, "c2": appC2, "l2": appL2, "c3": appC3, "l3": appL3})
	repo(regRepoOut,
		map[string]string{"v1": appM4, "v0": appM5},
		[]string{appM4, appM5},
		map[string]string{"c4": appC4, "l4": appL4, "l5": appL5})
	write("repositories/"+regRepoIn+"/_uploads/u1/data", "UPLOAD-IN-CORSO")
}

// La regola «immagini in uso dalle app sempre incluse» (H4, v3): versione attiva e precedente,
// tag per tag; il resto del registry (tag v3 e v0, manifest e blob non referenziati) resta fuori.
func TestBackupImmaginiInUsoAttivaEPrecedente(t *testing.T) {
	e := newEnv(t)
	seedAppRegistry(t, e.git)
	e.o.InUseImages = func(context.Context) ([]InUseImage, error) {
		return []InUseImage{
			{RepoID: regRepoIn, Owner: "alice", Repo: "web", Reference: "registry.gitstack.test/alice/web:v2"},
			{RepoID: regRepoIn, Owner: "alice", Repo: "web", Reference: "registry.gitstack.test/alice/web:v1"},
			{RepoID: regRepoOut, Owner: "bob", Repo: "api", Reference: "registry.gitstack.test/bob/api:v1"},
		}, nil
	}
	res, err := Backup(context.Background(), e.o)
	if err != nil {
		t.Fatal(err)
	}
	names := tarNames(t, res.Path, GitDataName)
	v2 := "registry/docker/registry/v2/"
	wantIn := []string{
		v2 + "repositories/" + regRepoIn + "/_manifests/tags/v1/current/link",
		v2 + "repositories/" + regRepoIn + "/_manifests/tags/v2/current/link",
		v2 + "repositories/" + regRepoIn + "/_manifests/revisions/sha256/" + appM1 + "/link",
		v2 + "repositories/" + regRepoIn + "/_manifests/revisions/sha256/" + appI + "/link",
		v2 + "repositories/" + regRepoIn + "/_manifests/revisions/sha256/" + appM2 + "/link", // figlio dell'indice
		v2 + "repositories/" + regRepoIn + "/_layers/sha256/" + appL1 + "/link",
		v2 + "repositories/" + regRepoIn + "/_layers/sha256/" + appL2 + "/link",
		v2 + "repositories/" + regRepoOut + "/_manifests/tags/v1/current/link",
		v2 + "repositories/" + regRepoOut + "/_manifests/revisions/sha256/" + appM4 + "/link",
		v2 + "blobs/sha256/" + appM1[:2] + "/" + appM1 + "/data",
		v2 + "blobs/sha256/" + appM2[:2] + "/" + appM2 + "/data",
		v2 + "blobs/sha256/" + appI[:2] + "/" + appI + "/data",
		v2 + "blobs/sha256/" + appC1[:2] + "/" + appC1 + "/data",
		v2 + "blobs/sha256/" + appC2[:2] + "/" + appC2 + "/data",
		v2 + "blobs/sha256/" + appL1[:2] + "/" + appL1 + "/data",
		v2 + "blobs/sha256/" + appL2[:2] + "/" + appL2 + "/data",
		v2 + "blobs/sha256/" + appM4[:2] + "/" + appM4 + "/data",
		v2 + "blobs/sha256/" + appC4[:2] + "/" + appC4 + "/data",
		v2 + "blobs/sha256/" + appL4[:2] + "/" + appL4 + "/data",
	}
	for _, w := range wantIn {
		if !hasSuffixAny(names, w) {
			t.Errorf("manca %s", w)
		}
	}
	for _, frag := range []string{
		"/_manifests/tags/v3", "/_manifests/tags/v0", // tag non in uso
		"/_manifests/tags/v2/index",                // storia dei tag
		"/_manifests/revisions/sha256/" + appM3,    // manifest non in uso
		"/_layers/sha256/" + appL3,                 // layer non in uso
		"/_layers/sha256/" + appL5,                 // layer di v0
		"/blobs/sha256/" + appM5[:2] + "/" + appM5, // manifest di v0
		"/blobs/sha256/" + appC3[:2] + "/" + appC3, // config di v3
		"/blobs/sha256/" + appL5[:2] + "/" + appL5, // blob di v0
		"_uploads",
	} {
		for _, n := range names {
			if strings.Contains(n, frag) {
				t.Errorf("non doveva entrare (%s): %s", frag, n)
			}
		}
	}
}

// Dopo il restore su un'installazione pulita le immagini in uso tornano con i loro tag, e le app
// possono ripartire (H4, criterio di GIT-351).
func TestRestoreImmaginiInUsoTornanoConITag(t *testing.T) {
	e := newEnv(t)
	seedAppRegistry(t, e.git)
	e.o.InUseImages = func(context.Context) ([]InUseImage, error) {
		return []InUseImage{{RepoID: regRepoIn, Owner: "alice", Repo: "web", Reference: "registry.gitstack.test/alice/web:v2"}}, nil
	}
	res, err := Backup(context.Background(), e.o)
	if err != nil {
		t.Fatal(err)
	}
	r := newEnv(t)
	r.o.DestDir = filepath.Join(t.TempDir(), "b")
	if err := Restore(context.Background(), r.o, res.Path); err != nil {
		t.Fatal(err)
	}
	v2 := filepath.Join(r.git, "registry", "docker", "registry", "v2")
	for _, p := range []string{
		filepath.Join(v2, "repositories", regRepoIn, "_manifests", "tags", "v2", "current", "link"),
		filepath.Join(v2, "repositories", regRepoIn, "_manifests", "revisions", "sha256", appM2, "link"),
		filepath.Join(v2, "blobs", "sha256", appL2[:2], appL2, "data"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("dopo il restore manca %s: %v", p, err)
		}
	}
	for _, p := range []string{
		filepath.Join(v2, "repositories", regRepoIn, "_manifests", "tags", "v3"),
		filepath.Join(v2, "blobs", "sha256", appL3[:2], appL3),
	} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("dopo il restore non doveva esserci %s", p)
		}
	}
}

// Un'immagine in uso che manca dal registry ferma il backup: un'app che non riparte non deve
// passare inosservata.
func TestBackupImmagineInUsoMancanteFermaIlBackup(t *testing.T) {
	e := newEnv(t)
	seedAppRegistry(t, e.git)
	e.o.InUseImages = func(context.Context) ([]InUseImage, error) {
		return []InUseImage{{RepoID: regRepoIn, Owner: "alice", Repo: "web", Reference: "registry.gitstack.test/alice/web:v9"}}, nil
	}
	_, err := Backup(context.Background(), e.o)
	if err == nil || !strings.Contains(err.Error(), "v9") {
		t.Fatalf("atteso errore sul tag v9 mancante, ottenuto %v", err)
	}
}

// Senza hook le immagini in uso vengono dal database (core.apps, core.app_versions): una riga per
// immagine, come le restituisce psql. Il manifest ne porta i riferimenti.
func TestInUseDalDatabase(t *testing.T) {
	e := newEnv(t)
	seedAppRegistry(t, e.git)
	e.f.inUse = regRepoIn + "|alice|web|registry.gitstack.test/alice/web:v2|\n" +
		regRepoIn + "|alice|web|registry.gitstack.test/alice/web:v1|\n"
	res, err := Backup(context.Background(), e.o)
	if err != nil {
		t.Fatal(err)
	}
	names := tarNames(t, res.Path, GitDataName)
	if !hasSuffixAny(names, "repositories/"+regRepoIn+"/_manifests/tags/v2/current/link") || hasSuffixAny(names, "repositories/"+regRepoIn+"/_manifests/tags/v3/current/link") {
		t.Fatalf("tag in uso dal database: %v", names)
	}
	if len(res.Manifest.AppImages) != 2 || len(res.Manifest.RegistryRepos) != 0 {
		t.Errorf("manifest: app_images=%v registry_repos=%v", res.Manifest.AppImages, res.Manifest.RegistryRepos)
	}
}

// Il riferimento salvato dall'app va letto senza ambiguità: solo i riferimenti del repo, con
// tag o digest validi. Ogni caso è un input quasi valido.
func TestParseInUse(t *testing.T) {
	d := "sha256:" + appM1
	cases := []struct {
		name string
		img  InUseImage
		ok   bool
		want imageSel
	}{
		{"tag sul repo", InUseImage{RepoID: regRepoIn, Owner: "alice", Repo: "web", Reference: "registry.gitstack.test/alice/web:v2"}, true,
			imageSel{repoID: regRepoIn, tag: "v2"}},
		{"nome annidato e tag", InUseImage{RepoID: regRepoIn, Owner: "alice", Repo: "web", Reference: "registry.gitstack.test/alice/web/worker:1.0"}, true,
			imageSel{repoID: regRepoIn, name: "worker", tag: "1.0"}},
		{"solo digest", InUseImage{RepoID: regRepoIn, Owner: "alice", Repo: "web", Reference: "registry.gitstack.test/alice/web@" + d}, true,
			imageSel{repoID: regRepoIn, digest: appM1}},
		{"digest dalla colonna", InUseImage{RepoID: regRepoIn, Owner: "alice", Repo: "web", Reference: "registry.gitstack.test/alice/web:v2", Digest: d}, true,
			imageSel{repoID: regRepoIn, tag: "v2", digest: appM1}},
		{"repo con prefisso nel nome", InUseImage{RepoID: regRepoIn, Owner: "alice", Repo: "web", Reference: "registry.gitstack.test/alice/webx:v1"}, false, imageSel{}},
		{"senza tag né digest", InUseImage{RepoID: regRepoIn, Owner: "alice", Repo: "web", Reference: "registry.gitstack.test/alice/web"}, false, imageSel{}},
		{"digest non esadecimale", InUseImage{RepoID: regRepoIn, Owner: "alice", Repo: "web", Reference: "registry.gitstack.test/alice/web@sha256:xyz"}, false, imageSel{}},
		{"nome maiuscolo", InUseImage{RepoID: regRepoIn, Owner: "alice", Repo: "web", Reference: "registry.gitstack.test/alice/web/Worker:v1"}, false, imageSel{}},
		{"tag con percorso", InUseImage{RepoID: regRepoIn, Owner: "alice", Repo: "web", Reference: "registry.gitstack.test/alice/web:../x"}, false, imageSel{}},
		{"altro repo", InUseImage{RepoID: regRepoIn, Owner: "carol", Repo: "other", Reference: "registry.gitstack.test/alice/web:v1"}, false, imageSel{}},
		{"id del repo non valido", InUseImage{RepoID: "nope", Owner: "alice", Repo: "web", Reference: "registry.gitstack.test/alice/web:v1"}, false, imageSel{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseInUse(c.img)
			if c.ok != (err == nil) {
				t.Fatalf("errore = %v, atteso ok=%v", err, c.ok)
			}
			if c.ok && got != c.want {
				t.Errorf("= %+v, atteso %+v", got, c.want)
			}
		})
	}
}
