//go:build integration

package stackitest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// R11 rivista (GIT-178): il nome del repo conserva le maiuscole, l'unicità per
// owner non le distingue e ogni indirizzo risolve il repo in qualsiasi
// combinazione. Stack completo: gateway, identity, core, servizio git, Postgres,
// git e ssh veri.
func TestNomeRepoMaiuscole(t *testing.T) {
	e := newGitEnv(t)
	repoID := e.createRepo("alice", "GitStack", "private")

	t.Run("nome_conservato_e_unicita_senza_maiuscole", func(t *testing.T) {
		r := e.gw("GET", "/repos/alice/GitStack", nil, e.bearer("alice"), nil)
		want(t, r, 200, "")
		if r.json()["name"] != "GitStack" || r.json()["fullName"] != "alice/GitStack" {
			t.Errorf("nome salvato: %v %v", r.json()["name"], r.json()["fullName"])
		}
		urls, _ := r.json()["cloneUrls"].(map[string]any)
		if urls["https"] != e.gitHTTP+"/alice/GitStack.git" || !strings.HasSuffix(urls["ssh"].(string), "/alice/GitStack.git") {
			t.Errorf("cloneUrls = %v", urls)
		}
		for _, dup := range []string{"gitstack", "GITSTACK", "gitStack"} {
			c := e.gw("POST", "/repos", map[string]any{"owner": "alice", "name": dup}, e.bearer("alice"), nil)
			want(t, c, 409, "conflict")
		}
		// L'elenco mostra il nome come scritto alla creazione.
		l := e.gw("GET", "/repos?owner=alice", nil, e.bearer("alice"), nil)
		want(t, l, 200, "")
		if !strings.Contains(string(l.body), `"fullName":"alice/GitStack"`) || strings.Contains(string(l.body), "alice/gitstack") {
			t.Errorf("elenco: %s", l.body)
		}
		// Un nome che finisce in .git, in qualsiasi maiuscola, resta vietato.
		want(t, e.gw("POST", "/repos", map[string]any{"owner": "alice", "name": "x.GIT"}, e.bearer("alice"), nil), 400, "invalid_name")
	})

	// Contenuto: pusha come alice con il nome canonico.
	httpsURL := func(name string) string { return e.httpsURL("alice", e.tokens["alice"], "/alice/"+name+".git") }
	work := e.initWork(httpsURL("GitStack"), map[string]string{"README.md": "# GitStack\n", "dir/note.txt": "ciao\n"})
	e.mustGit(work, "", "push", "-q", "-u", "origin", "main")

	for i, name := range []string{"GitStack", "gitstack", "GITSTACK"} {
		file := fmt.Sprint(i) // nome file senza maiuscole: su Windows non distingue
		t.Run("https_clone_push_"+name, func(t *testing.T) {
			dir := e.clone(httpsURL(name), "")
			if _, err := os.Stat(filepath.Join(dir, "dir", "note.txt")); err != nil {
				t.Fatalf("clone con %s: %v", name, err)
			}
			e.commitFile(dir, "https-"+file+".txt", "x\n", "da https "+name)
			e.mustGit(dir, "", "push", "-q", "origin", "main")
			e.mustGit(work, "", "pull", "-q", "--ff-only", "origin", "main")
			if _, err := os.Stat(filepath.Join(work, "https-"+file+".txt")); err != nil {
				t.Fatalf("il push con %s non è arrivato: %v", name, err)
			}
		})
		t.Run("ssh_clone_push_"+name, func(t *testing.T) {
			dir := e.clone(e.sshURL("/alice/"+name+".git"), e.keys["alice"])
			if _, err := os.Stat(filepath.Join(dir, "dir", "note.txt")); err != nil {
				t.Fatalf("clone SSH con %s: %v", name, err)
			}
			e.commitFile(dir, "ssh-"+file+".txt", "x\n", "da ssh "+name)
			e.mustGit(dir, e.keys["alice"], "push", "-q", "origin", "main")
			e.mustGit(work, "", "pull", "-q", "--ff-only", "origin", "main")
			if _, err := os.Stat(filepath.Join(work, "ssh-"+file+".txt")); err != nil {
				t.Fatalf("il push SSH con %s non è arrivato: %v", name, err)
			}
		})
		t.Run("api_raw_archivi_"+name, func(t *testing.T) {
			base := "/repos/alice/" + name
			r := e.gw("GET", base, nil, e.bearer("alice"), nil)
			want(t, r, 200, "")
			if r.json()["name"] != "GitStack" || r.json()["id"] != repoID {
				t.Errorf("GET %s: name=%v id=%v", base, r.json()["name"], r.json()["id"])
			}
			for _, p := range []string{"/tree", "/readme", "/commits", "/raw?path=dir/note.txt", "/raw/main/dir/note.txt", "/archive?ref=main", "/archive?ref=main&format=tar.gz"} {
				got := e.gw("GET", base+p, nil, e.bearer("alice"), nil)
				want(t, got, 200, "")
				if len(got.body) == 0 {
					t.Errorf("%s%s: corpo vuoto", base, p)
				}
			}
			if raw := e.gw("GET", base+"/raw?path=dir/note.txt", nil, e.bearer("alice"), nil); string(raw.body) != "ciao\n" {
				t.Errorf("raw = %q", raw.body)
			}
		})
	}

	t.Run("permessi_non_dipendono_dalla_forma", func(t *testing.T) {
		// bob non ha grant: 404 con ogni forma, e il clone fallisce.
		for _, name := range []string{"GitStack", "gitstack", "GITSTACK"} {
			want(t, e.gw("GET", "/repos/alice/"+name, nil, e.bearer("bob"), nil), 404, "")
			want(t, e.gw("GET", "/repos/alice/"+name+"/raw?path=README.md", nil, e.bearer("bob"), nil), 404, "")
			status, _, _ := e.httpGet("/alice/"+name+".git"+upload, "bob", e.tokens["bob"])
			if status != 404 {
				t.Errorf("info/refs di bob su %s: %d, voluto 404", name, status)
			}
		}
		// e un token di sola lettura di alice non scrive con nessuna forma.
		dir := e.clone(e.httpsURL("alice", e.roToken["alice"], "/alice/gitstack.git"), "")
		e.commitFile(dir, "ro.txt", "x\n", "solo lettura")
		if out, err := e.git(dir, "", "push", "-q", "origin", "main"); err == nil {
			t.Errorf("push con token di sola lettura riuscito: %s", out)
		}
	})
}

// R11 rivista (GIT-178) in `gs`: il nome creato con le maiuscole resta tale e
// `owner/repo` si scrive in qualsiasi forma.
func TestGsNomeRepoMaiuscole(t *testing.T) {
	g := newGsEnv(t)
	alice := gsOpts{user: "alice"}

	r := repoOK(g, alice, "repo", "create", "GitStack", "--json", "name,fullName,url")
	m := jsonMap(t, r.out)
	if m["name"] != "GitStack" || m["fullName"] != "alice/GitStack" || !strings.HasSuffix(fmt.Sprint(m["url"]), "/alice/GitStack") {
		t.Errorf("repo creato: %v", m)
	}
	// Lo stesso nome con altre maiuscole è già usato: exit 1.
	repoExit(g, 1, alice, "repo", "create", "gitstack")
	for _, form := range []string{"alice/GitStack", "alice/gitstack", "alice/GITSTACK"} {
		v := jsonMap(t, repoOK(g, alice, "repo", "view", form, "--json", "fullName").out)
		if v["fullName"] != "alice/GitStack" {
			t.Errorf("gs repo view %s: %v", form, v)
		}
	}
	// --repo accetta qualsiasi forma.
	repoOK(g, alice, "issue", "list", "--repo", "alice/GITSTACK")
	// clone con la forma minuscola: la cartella ha il nome come scritto dall'utente, il remote risponde.
	dir := filepath.Join(t.TempDir(), "w")
	repoOK(g, alice, "repo", "clone", "alice/gitstack", dir)
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Errorf("clone: %v", err)
	}
}
