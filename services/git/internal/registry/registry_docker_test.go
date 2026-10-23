//go:build docker

package registry

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Questo test usa il client `docker` vero: login, push e pull contro il registry di GitStack
// (criterio di GIT-286). Non fa parte di `go test ./...`: serve il tag `docker`, un demone
// Docker raggiungibile e il registry in ascolto su 127.0.0.1, che docker tratta come non sicuro
// senza configurazione (su Linux, e in CI, il demone è sullo stesso host).
//
//	go test -tags docker -run TestDocker -count=1 ./internal/registry/

type dockerEnv struct {
	t      *testing.T
	config string
}

func (d *dockerEnv) run(stdin []byte, args ...string) (string, error) {
	d.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = append(os.Environ(), "DOCKER_CONFIG="+d.config)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (d *dockerEnv) must(args ...string) string {
	d.t.Helper()
	out, err := d.run(nil, args...)
	if err != nil {
		d.t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// fails: il comando deve fallire e l'uscita contenere uno dei testi.
func (d *dockerEnv) fails(what string, want []string, stdin []byte, args ...string) {
	d.t.Helper()
	out, err := d.run(stdin, args...)
	if err == nil {
		d.t.Errorf("%s: doveva fallire\n%s", what, out)
		return
	}
	for _, w := range want {
		if strings.Contains(strings.ToLower(out), strings.ToLower(w)) {
			return
		}
	}
	d.t.Errorf("%s: fallito, ma l'uscita non contiene nessuno di %q:\n%s", what, want, out)
}

func (d *dockerEnv) login(host, tok string) (string, error) {
	return d.run([]byte(tok), "login", host, "-u", "token", "--password-stdin")
}

func rootfs(t *testing.T, content string) []byte {
	t.Helper()
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	if err := tw.WriteHeader(&tar.Header{Name: "hello.txt", Mode: 0o644, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func requireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker non trovato nel PATH")
	}
	if out, err := exec.Command("docker", "version", "--format", "{{.Server.Version}}").CombinedOutput(); err != nil {
		t.Skipf("nessun demone Docker raggiungibile: %v %s", err, out)
	}
}

// Archiviazione su disco.
func TestDocker_LoginPushPull_Disco(t *testing.T) {
	requireDocker(t)
	dockerScenario(t, newEnv(t))
}

// Stessa prova con l'archiviazione su S3 (S3 finto).
func TestDocker_LoginPushPull_S3(t *testing.T) {
	requireDocker(t)
	fs3 := newFakeS3(t, "immagini")
	e := newEnvWith(t, func(c *Config) {
		c.Dir, c.testInMemory = "", false
		c.S3 = &S3{Bucket: "immagini", Endpoint: fs3.srv.URL, AccessKey: "AK", SecretKey: "SK", PathStyle: true, Insecure: true}
	})
	dockerScenario(t, e)
	if len(fs3.keys(t, "immagini", "gitstack-registry/")) == 0 {
		t.Error("nessun oggetto nel bucket S3 dopo i push di docker")
	}
}

func dockerScenario(t *testing.T, e *env) {
	host := e.host()
	d := &dockerEnv{t: t, config: t.TempDir()}

	// Un'immagine minuscola, senza scaricare nulla: il filesystem è un solo file.
	local := "gitstack-registry-test:" + strings.ToLower(strings.NewReplacer("/", "-", "_", "-").Replace(t.Name()))
	d.run(nil, "rmi", "-f", local)
	if out, err := d.run(rootfs(t, "ciao\n"), "import", "-", local); err != nil {
		t.Fatalf("docker import: %v\n%s", err, out)
	}
	t.Cleanup(func() { d.run(nil, "rmi", "-f", local) })
	remote := func(path string) string { return host + "/" + path }
	tag := func(path string) {
		t.Helper()
		d.must("tag", local, remote(path))
		t.Cleanup(func() { d.run(nil, "rmi", "-f", remote(path)) })
	}

	// Nessun accesso anonimo: né login senza token, né push o pull senza login.
	d.fails("pull anonimo", []string{"no basic auth credentials", "unauthorized", "authentication required"}, nil, "pull", remote("alice/app:anon"))
	tag("alice/app:anon")
	d.fails("push anonimo", []string{"no basic auth credentials", "unauthorized", "authentication required"}, nil, "push", remote("alice/app:anon"))

	// Token sbagliati: il login stesso fallisce.
	for _, tok := range []string{"gst_inventato", "gsj_inventato", "sbagliato"} {
		if out, err := d.login(host, tok); err == nil {
			t.Errorf("docker login con %q doveva fallire\n%s", tok, out)
		}
	}

	// write: pubblica e scarica.
	if out, err := d.login(host, "gst_write"); err != nil {
		t.Fatalf("login write: %v\n%s", err, out)
	}
	tag("alice/app/web:1.0")
	d.must("push", remote("alice/app/web:1.0"))
	d.must("rmi", remote("alice/app/web:1.0"))
	d.must("pull", remote("alice/app/web:1.0"))
	// Il ruolo write non elimina e il suo scope non è admin: non c'è niente da provare con docker
	// (il client non cancella); l'eliminazione è coperta dai test con un client OCI.

	// read: scarica, non pubblica.
	d.must("logout", host)
	if out, err := d.login(host, "gst_read"); err != nil {
		t.Fatalf("login read: %v\n%s", err, out)
	}
	d.must("rmi", remote("alice/app/web:1.0"))
	d.must("pull", remote("alice/app/web:1.0"))
	tag("alice/app/web:2.0")
	d.fails("push con read", []string{"denied", "write:registry", "forbidden"}, nil, "push", remote("alice/app/web:2.0"))

	// Scope di codice (write:resource) senza scope registry: non apre il registry.
	d.must("logout", host)
	if out, err := d.login(host, "gst_code"); err != nil {
		t.Fatalf("login code: %v\n%s", err, out)
	}
	d.fails("pull con scope resource", []string{"denied", "read:registry", "forbidden"}, nil, "pull", remote("alice/app/web:1.0"))

	// Repo non leggibile: come se non esistesse.
	d.must("logout", host)
	if _, err := d.login(host, "gst_read"); err != nil {
		t.Fatal(err)
	}
	d.fails("pull da un repo non leggibile", []string{"not found", "name unknown", "manifest unknown", "non trovato", "repository does not exist"}, nil, "pull", remote("alice/secret:1"))

	// Token del job: pubblica con packages write, solo il suo repo.
	d.must("logout", host)
	if out, err := d.login(host, "gsj_rw"); err != nil {
		t.Fatalf("login con il token del job: %v\n%s", err, out)
	}
	tag("alice/app/ci:1")
	d.must("push", remote("alice/app/ci:1"))
	tag("bob/other:1")
	d.fails("push del job su un altro repo", []string{"not found", "name unknown", "non trovato"}, nil, "push", remote("bob/other:1"))
	d.must("logout", host)
	if _, err := d.login(host, "gsj_ro"); err != nil {
		t.Fatal(err)
	}
	tag("alice/app/ci:2")
	d.fails("push con packages read", []string{"denied", "packages: write", "forbidden"}, nil, "push", remote("alice/app/ci:2"))
	d.must("rmi", remote("alice/app/ci:1"))
	d.must("pull", remote("alice/app/ci:1"))

	// Archiviato: si scarica, non si pubblica.
	d.must("logout", host)
	if _, err := d.login(host, "gst_admin"); err != nil {
		t.Fatal(err)
	}
	e.b.set("alice/app", func(r *fakeRepo) { r.archived = true })
	d.must("rmi", remote("alice/app/web:1.0"))
	d.must("pull", remote("alice/app/web:1.0"))
	tag("alice/app/web:3.0")
	d.fails("push su repo archiviato", []string{"denied", "archiv"}, nil, "push", remote("alice/app/web:3.0"))
	e.b.set("alice/app", func(r *fakeRepo) { r.archived = false })
	d.must("push", remote("alice/app/web:3.0"))

	// Eliminato e ripristinato: le immagini tornano.
	e.b.set("alice/app", func(r *fakeRepo) { r.deleted = true })
	d.must("rmi", remote("alice/app/web:1.0"))
	d.fails("pull da repo eliminato", []string{"not found", "name unknown", "manifest unknown", "non trovato", "repository does not exist"}, nil, "pull", remote("alice/app/web:1.0"))
	e.b.set("alice/app", func(r *fakeRepo) { r.deleted = false })
	d.must("pull", remote("alice/app/web:1.0"))
}
