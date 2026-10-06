//go:build integration

package stackitest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// M-07/D (GIT-165): `gs repo` sullo stack completo. Il binario gs è quello
// vero (go build di cli/cmd/gs) e parla col gateway vero attraverso il proxy
// /api/v1→/v1 di newGsEnv; `clone` lancia il git vero (HTTPS con un credential
// helper di prova, SSH con la chiave di alice e il server SSH del servizio git).
// Utenti: alice (admin di acme), bob, carol, dave; admin di installazione.

// gsOpts sono le variazioni di un lancio di gs.
type gsOpts struct {
	user  string // "" = token inventato
	stdin string
	ssh   string // chiave SSH per git (clone via SSH)
}

// repoGS lancia gs; non è un metodo di gsEnv per non toccare gs_issue_integration_test.go.
func repoGS(g *gsEnv, o gsOpts, args ...string) gsRun {
	g.t.Helper()
	token := "gst_inventato0000000000000000000000000000"
	if o.user != "" {
		token = g.tokens[o.user]
	}
	home := g.t.TempDir()
	// git di gs: nessun prompt, nessun helper di sistema; per HTTPS l'helper risponde col token di chi lancia.
	gitconfig := filepath.Join(home, ".gitconfig")
	helper := ""
	if o.user != "" {
		helper = fmt.Sprintf("[credential]\n\thelper = \"!f() { echo username=%s; echo password=%s; }; f\"\n", o.user, token)
	}
	if err := os.WriteFile(gitconfig, []byte("[init]\n\tdefaultBranch = main\n[core]\n\tautocrlf = false\n"+helper), 0o600); err != nil {
		g.t.Fatal(err)
	}
	sshCmd := "ssh -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=" + filepath.ToSlash(filepath.Join(home, "known_hosts")) + " -o LogLevel=ERROR -o IdentitiesOnly=yes"
	if o.ssh != "" {
		sshCmd += " -i " + o.ssh
	}
	cmd := exec.Command(g.bin, append([]string{"--hostname", g.proxy}, args...)...)
	cmd.Dir = g.t.TempDir() // fuori da un repo: niente remote origin
	cmd.Env = append(cleanEnv(), "GS_CONFIG_DIR="+g.t.TempDir(), "GS_TOKEN="+token, "HOME="+home, "USERPROFILE="+home,
		"GIT_CONFIG_GLOBAL="+gitconfig, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_ASKPASS=",
		"GIT_SSH_COMMAND="+sshCmd)
	cmd.Stdin = strings.NewReader(o.stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			g.t.Fatalf("gs %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return gsRun{out.String(), errOut.String(), code}
}

func repoOK(g *gsEnv, o gsOpts, args ...string) gsRun {
	g.t.Helper()
	r := repoGS(g, o, args...)
	if r.code != 0 {
		g.t.Fatalf("gs %v come %s: exit %d: %s", args, o.user, r.code, r.errOut)
	}
	return r
}

func repoExit(g *gsEnv, code int, o gsOpts, args ...string) gsRun {
	g.t.Helper()
	r := repoGS(g, o, args...)
	if r.code != code {
		g.t.Errorf("gs %v come %s: exit %d, voluto %d (%s)", args, o.user, r.code, code, r.errOut)
	}
	return r
}

func jsonMap(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("JSON non valido: %v: %s", err, s)
	}
	return m
}

// repoNames elenca i fullName di `gs repo list` con gli argomenti dati.
func repoNames(t *testing.T, g *gsEnv, user string, args ...string) []string {
	t.Helper()
	r := repoOK(g, gsOpts{user: user}, append([]string{"repo", "list", "--json", "fullName", "-L", "100"}, args...)...)
	var items []struct {
		FullName string `json:"fullName"`
	}
	if err := json.Unmarshal([]byte(r.out), &items); err != nil {
		t.Fatalf("JSON non valido: %v: %s", err, r.out)
	}
	var out []string
	for _, i := range items {
		out = append(out, i.FullName)
	}
	return out
}

func inList(list []string, s string) bool { return containsStr(list, s) }

func TestGsRepo(t *testing.T) {
	g := newGsEnv(t)
	alice := gsOpts{user: "alice"}
	bob := gsOpts{user: "bob"}

	// L'organizzazione acme: la crea l'admin di installazione, alice ne è owner.
	login := g.gw("POST", "/auth/login", map[string]any{"username": "admin", "password": newPassword}, nil, nil)
	want(t, login, 200, "")
	admin := g.session(login)
	want(t, g.gw("POST", "/orgs", map[string]any{"name": "acme"}, nil, admin), 201, "")
	want(t, g.gw("PUT", "/orgs/acme/members/alice", map[string]any{"role": "owner"}, nil, admin), 200, "")

	t.Run("create_privato_di_default_P7", func(t *testing.T) {
		r := repoOK(g, alice, "repo", "create", "demo", "--json", "fullName,visibility,owner,empty,url,cloneUrls,archived")
		m := jsonMap(t, r.out)
		if m["fullName"] != "alice/demo" || m["visibility"] != "private" || m["empty"] != true || m["archived"] != false {
			t.Errorf("repo creato: %v", m)
		}
		if o, _ := m["owner"].(map[string]any); o["name"] != "alice" || o["type"] != "user" {
			t.Errorf("owner = %v", m["owner"])
		}
		if !strings.HasSuffix(fmt.Sprint(m["url"]), "/alice/demo") {
			t.Errorf("url = %v", m["url"])
		}
		// l'API conferma: privato; chi non ha un grant non lo vede (404, non 403)
		api := g.api("alice", "GET", "/repos/alice/demo", nil)
		want(t, api, 200, "")
		if api.json()["visibility"] != "private" {
			t.Errorf("API: %v", api.json()["visibility"])
		}
		want(t, g.api("bob", "GET", "/repos/alice/demo", nil), 404, "not_found")
		// stdout senza --json: l'indirizzo web
		if r := repoOK(g, alice, "repo", "create", "altro"); !strings.HasSuffix(strings.TrimSpace(r.out), "/alice/altro") {
			t.Errorf("stdout = %q", r.out)
		}
		// nome già preso: 409, exit 1
		repoExit(g, 1, alice, "repo", "create", "demo")
		// un repo non è mai pubblico: uso errato
		repoExit(g, 2, alice, "repo", "create", "pub", "--visibility", "public")
	})

	t.Run("create_contenuto_iniziale_R5", func(t *testing.T) {
		repoOK(g, alice, "repo", "create", "tool", "--internal", "-d", "Strumento", "--add-readme", "--gitignore", "go", "--license", "mit")
		m := jsonMap(t, repoOK(g, alice, "repo", "view", "alice/tool", "--json", "visibility,empty,description,defaultBranch").out)
		if m["visibility"] != "internal" || m["empty"] != false || m["description"] != "Strumento" || m["defaultBranch"] != "main" {
			t.Errorf("tool: %v", m)
		}
		dir := filepath.Join(t.TempDir(), "tool")
		repoOK(g, alice, "repo", "clone", "alice/tool", dir)
		for _, f := range []string{"README.md", ".gitignore", "LICENSE"} {
			if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
				t.Errorf("manca %s: %v", f, err)
			}
		}
		if n := strings.TrimSpace(g.mustGit(dir, "", "rev-list", "--count", "main")); n != "1" {
			t.Errorf("primo commit su main: %s commit", n)
		}
		// senza flag il repo è vuoto: nessun file iniziale
		if m := jsonMap(t, repoOK(g, alice, "repo", "view", "alice/altro", "--json", "empty").out); m["empty"] != true {
			t.Errorf("altro: %v", m)
		}
	})

	t.Run("create_organizzazione", func(t *testing.T) {
		r := repoOK(g, alice, "repo", "create", "acme/web", "--json", "fullName,owner,visibility")
		m := jsonMap(t, r.out)
		if o, _ := m["owner"].(map[string]any); m["fullName"] != "acme/web" || o["type"] != "organization" || m["visibility"] != "private" {
			t.Errorf("acme/web: %v", m)
		}
		repoOK(g, alice, "repo", "create", "api", "--owner", "acme", "--internal")
		// chi non è nell'organizzazione non ci crea repo
		if r := repoGS(g, bob, "repo", "create", "acme/intruso"); r.code != 5 && r.code != 6 {
			t.Errorf("bob in acme: exit %d (%s)", r.code, r.errOut)
		}
	})

	t.Run("list", func(t *testing.T) {
		all := repoNames(t, g, "alice")
		for _, n := range []string{"alice/demo", "alice/tool", "acme/web", "acme/api"} {
			if !inList(all, n) {
				t.Errorf("manca %s in %v", n, all)
			}
		}
		if mine := repoNames(t, g, "alice", "alice"); inList(mine, "acme/web") || !inList(mine, "alice/demo") {
			t.Errorf("list alice: %v", mine)
		}
		if org := repoNames(t, g, "alice", "acme"); len(org) != 2 || !inList(org, "acme/web") {
			t.Errorf("list acme: %v", org)
		}
		if in := repoNames(t, g, "alice", "alice", "--visibility", "internal"); inList(in, "alice/demo") || !inList(in, "alice/tool") {
			t.Errorf("--visibility internal: %v", in)
		}
		// bob non vede i privati altrui, vede gli interni
		if b := repoNames(t, g, "bob"); inList(b, "alice/demo") || inList(b, "acme/web") || !inList(b, "alice/tool") || !inList(b, "acme/api") {
			t.Errorf("list bob: %v", b)
		}
		// senza --json: una riga per repo, colonne con tab
		r := repoOK(g, alice, "repo", "list", "acme")
		if !strings.Contains(r.out, "acme/web\tprivate\tattivo") {
			t.Errorf("tabella: %q", r.out)
		}
		if r := repoOK(g, alice, "repo", "list", "--json", "fullName", "-L", "1"); len(strings.Split(strings.TrimSpace(strings.Trim(r.out, "[]")), "},{")) != 1 {
			t.Errorf("-L 1: %s", r.out)
		}
	})

	t.Run("view", func(t *testing.T) {
		m := jsonMap(t, repoOK(g, alice, "repo", "view", "alice/demo", "--json", "id,name,fullName,description,visibility,archived,defaultBranch,empty,protectDefaultBranch,cloneUrls,createdAt,updatedAt,url").out)
		urls, _ := m["cloneUrls"].(map[string]any)
		if urls["https"] != g.gitHTTP+"/alice/demo.git" || !strings.HasPrefix(fmt.Sprint(urls["ssh"]), "ssh://git@127.0.0.1:") ||
			!strings.HasSuffix(fmt.Sprint(urls["ssh"]), "/alice/demo.git") {
			t.Errorf("cloneUrls = %v", urls)
		}
		r := repoOK(g, alice, "repo", "view", "alice/demo")
		for _, s := range []string{"alice/demo", "Visibilità: private", "Clone HTTPS: " + g.gitHTTP + "/alice/demo.git", "Clone SSH: ssh://git@127.0.0.1:"} {
			if !strings.Contains(r.out, s) {
				t.Errorf("view senza %q:\n%s", s, r.out)
			}
		}
		// campi sconosciuti: uso errato; --json senza campi li elenca
		repoExit(g, 2, alice, "repo", "view", "alice/demo", "--json", "nope")
		if r := repoOK(g, alice, "repo", "view", "--json"); !strings.Contains(r.out, "cloneUrls") {
			t.Errorf("campi: %s", r.out)
		}
		// codici di G3: 6 (privato altrui: 404, non 403), 4 (token inventato), 2 (senza repo)
		repoExit(g, 6, bob, "repo", "view", "alice/demo")
		repoExit(g, 6, alice, "repo", "view", "alice/non-esiste")
		repoExit(g, 4, gsOpts{}, "repo", "view", "alice/demo")
		repoExit(g, 2, alice, "repo", "view")
	})

	t.Run("clone_https_e_ssh", func(t *testing.T) {
		g.createRepoContent(t, "alice", "tool")
		https := filepath.Join(t.TempDir(), "https")
		repoOK(g, alice, "repo", "clone", "alice/tool", https)
		if b, err := os.ReadFile(filepath.Join(https, "dal-test.txt")); err != nil || string(b) != "ciao\n" {
			t.Fatalf("clone HTTPS: %v %q", err, b)
		}
		if u := strings.TrimSpace(g.mustGit(https, "", "remote", "get-url", "origin")); u != g.gitHTTP+"/alice/tool.git" {
			t.Errorf("origin HTTPS = %s", u)
		}
		ssh := filepath.Join(t.TempDir(), "ssh")
		repoOK(g, gsOpts{user: "alice", ssh: g.keys["alice"]}, "repo", "clone", "alice/tool", ssh, "--protocol", "ssh")
		if _, err := os.Stat(filepath.Join(ssh, "dal-test.txt")); err != nil {
			t.Fatalf("clone SSH: %v", err)
		}
		if u := strings.TrimSpace(g.mustGit(ssh, "", "remote", "get-url", "origin")); u != g.sshURL("/alice/tool.git") && !strings.HasPrefix(u, "ssh://git@127.0.0.1:") {
			t.Errorf("origin SSH = %s", u)
		}
		// opzioni di git dopo `--`, repo che non si legge, protocollo sconosciuto
		shallow := filepath.Join(t.TempDir(), "shallow")
		repoOK(g, alice, "repo", "clone", "alice/tool", shallow, "--", "--depth", "1")
		if _, err := os.Stat(filepath.Join(shallow, ".git", "shallow")); err != nil {
			t.Errorf("--depth 1 non applicato: %v", err)
		}
		repoExit(g, 6, bob, "repo", "clone", "alice/demo", filepath.Join(t.TempDir(), "x"))
		repoExit(g, 2, alice, "repo", "clone", "alice/tool", "--protocol", "ftp")
	})

	t.Run("edit", func(t *testing.T) {
		m := jsonMap(t, repoOK(g, alice, "repo", "edit", "alice/demo", "-d", "Nuova descrizione", "--visibility", "internal", "--protect-default-branch=false",
			"--json", "description,visibility,protectDefaultBranch").out)
		if m["description"] != "Nuova descrizione" || m["visibility"] != "internal" || m["protectDefaultBranch"] != false {
			t.Errorf("edit: %v", m)
		}
		// ora è interno: bob lo legge, ma non lo modifica (serve admin)
		want(t, g.api("bob", "GET", "/repos/alice/demo", nil), 200, "")
		repoExit(g, 5, bob, "repo", "edit", "alice/demo", "-d", "mia")
		repoExit(g, 2, alice, "repo", "edit", "alice/demo")
		repoOK(g, alice, "repo", "edit", "alice/demo", "--visibility", "private", "--protect-default-branch")
		if m := jsonMap(t, repoOK(g, alice, "repo", "view", "alice/demo", "--json", "visibility,protectDefaultBranch,description").out); m["visibility"] != "private" || m["protectDefaultBranch"] != true || m["description"] != "Nuova descrizione" {
			t.Errorf("dopo edit: %v", m)
		}
	})

	t.Run("archive_G8_e_R10", func(t *testing.T) {
		url := g.httpsURL("alice", g.tokens["alice"], "/alice/tool.git")
		w := g.clone(url, "")
		// senza TTY e senza --yes: exit 2, anche con il nome su stdin; il repo resta attivo
		repoExit(g, 2, gsOpts{user: "alice", stdin: "alice/tool\n"}, "repo", "archive", "alice/tool")
		if m := jsonMap(t, repoOK(g, alice, "repo", "view", "alice/tool", "--json", "archived").out); m["archived"] != false {
			t.Fatalf("archiviato senza conferma: %v", m)
		}
		// chi non è admin: 403 → 5 (con --yes: la conferma non salta i permessi)
		repoExit(g, 5, bob, "repo", "archive", "alice/tool", "--yes")
		r := repoOK(g, alice, "repo", "archive", "alice/tool", "--yes", "--json", "archived,archivedAt")
		if m := jsonMap(t, r.out); m["archived"] != true || m["archivedAt"] == nil {
			t.Errorf("archive: %v", m)
		}
		if l := repoNames(t, g, "alice", "alice", "--archived"); len(l) != 1 || l[0] != "alice/tool" {
			t.Errorf("list --archived: %v", l)
		}
		if l := repoNames(t, g, "alice", "alice", "--no-archived"); inList(l, "alice/tool") {
			t.Errorf("list --no-archived: %v", l)
		}
		// R10: si legge ancora, non si scrive
		g.commitFile(w, "dopo.txt", "x\n", "dopo archivio")
		if out, err := g.git(w, "", "push", "origin", "main"); err == nil || !strings.Contains(out, "403") {
			t.Fatalf("push su repo archiviato: err=%v\n%s", err, out)
		}
		repoOK(g, alice, "repo", "clone", "alice/tool", filepath.Join(t.TempDir(), "ancora-leggibile"))
		// unarchive: torna scrivibile, senza conferma
		if m := jsonMap(t, repoOK(g, alice, "repo", "unarchive", "alice/tool", "--json", "archived").out); m["archived"] != false {
			t.Errorf("unarchive: %v", m)
		}
		g.mustGit(w, "", "push", "-q", "origin", "main")
	})

	t.Run("delete_restore_G8_R2", func(t *testing.T) {
		// senza TTY e senza --yes: exit 2, nulla cambia
		repoExit(g, 2, gsOpts{user: "alice", stdin: "alice/demo\n"}, "repo", "delete", "alice/demo")
		want(t, g.api("alice", "GET", "/repos/alice/demo", nil), 200, "")
		// bob non è admin: 5, e il repo resta
		repoExit(g, 5, bob, "repo", "delete", "alice/tool", "--yes")
		want(t, g.api("alice", "GET", "/repos/alice/tool", nil), 200, "")

		r := repoOK(g, alice, "repo", "delete", "alice/demo", "--yes")
		if !strings.Contains(r.errOut, "gs repo restore alice/demo") {
			t.Errorf("messaggio: %q", r.errOut)
		}
		want(t, g.api("alice", "GET", "/repos/alice/demo", nil), 404, "not_found")
		if l := repoNames(t, g, "alice", "alice"); inList(l, "alice/demo") {
			t.Errorf("l'eliminato è ancora in list: %v", l)
		}
		repoExit(g, 6, alice, "repo", "view", "alice/demo")

		var del []map[string]any
		if err := json.Unmarshal([]byte(repoOK(g, alice, "repo", "list", "--deleted", "--json", "fullName,deletedAt,purgeAt,id").out), &del); err != nil {
			t.Fatal(err)
		}
		if len(del) != 1 || del[0]["fullName"] != "alice/demo" || del[0]["purgeAt"] == nil || del[0]["id"] == nil {
			t.Errorf("list --deleted: %v", del)
		}
		if r := repoOK(g, alice, "repo", "list", "--deleted"); !strings.HasPrefix(r.out, "alice/demo\t") {
			t.Errorf("tabella degli eliminati: %q", r.out)
		}
		// restore: torna con le impostazioni di prima
		m := jsonMap(t, repoOK(g, alice, "repo", "restore", "alice/demo", "--json", "fullName,visibility,description").out)
		if m["fullName"] != "alice/demo" || m["visibility"] != "private" || m["description"] != "Nuova descrizione" {
			t.Errorf("restore: %v", m)
		}
		want(t, g.api("alice", "GET", "/repos/alice/demo", nil), 200, "")
		// non c'è più niente da recuperare
		repoExit(g, 6, alice, "repo", "restore", "alice/demo")
		repoExit(g, 2, alice, "repo", "restore")
	})
}

// createRepoContent spinge un file in un repo esistente (per il clone).
func (g *gsEnv) createRepoContent(t *testing.T, owner, name string) {
	t.Helper()
	url := g.httpsURL(owner, g.tokens[owner], "/"+owner+"/"+name+".git")
	w := g.clone(url, "")
	g.commitFile(w, "dal-test.txt", "ciao\n", "file per il clone")
	g.mustGit(w, "", "push", "-q", "origin", "main")
}
