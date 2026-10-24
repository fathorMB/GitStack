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

// La regola «immagini in uso dalle app sempre incluse» (H4, v3): preparata con InUseRepoIDs.
func TestBackupImmaginiInUsoDalleAppSempreIncluse(t *testing.T) {
	e := newEnv(t)
	seedRegistry(t, e.git)
	e.o.InUseRepoIDs = func(context.Context) ([]string, error) { return []string{regRepoOut}, nil }
	res, err := Backup(context.Background(), e.o)
	if err != nil {
		t.Fatal(err)
	}
	names := tarNames(t, res.Path, GitDataName)
	if !hasSuffixAny(names, "repositories/"+regRepoOut+"/_manifests/tags/v1/current/link") || !hasSuffixAny(names, regOnlyOut+"/data") {
		t.Fatalf("le immagini in uso devono entrare: %v", names)
	}
	if hasSuffixAny(names, regOnlyIn+"/data") {
		t.Errorf("le altre restano fuori: %v", names)
	}
}
