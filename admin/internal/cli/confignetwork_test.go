package cli

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/admin/internal/config"
)

// netRunner registra i comandi; `k3s kubectl get nodes` risponde Ready.
type netRunner struct {
	mu      sync.Mutex
	calls   []call
	helmErr error
}

func (r *netRunner) Run(_ context.Context, env []string, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call{name, args, env})
	switch {
	case strings.HasSuffix(name, "helm"):
		return nil, r.helmErr
	case name == "k3s" && len(args) > 1 && args[1] == "get":
		return []byte("node1   Ready   control-plane   1d   v1.36.4\n"), nil
	case name == "k3s" && len(args) > 1 && args[1] == "create":
		return []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n"), nil
	}
	return nil, nil
}

func (r *netRunner) names() []string {
	var out []string
	for _, c := range r.calls {
		out = append(out, c.name+" "+strings.Join(c.args, " "))
	}
	return out
}

func (r *netRunner) has(sub string) bool {
	for _, n := range r.names() {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

func testCAPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Corp Test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

type netFixture struct {
	cfg, dir string
	paths    networkPaths
	app      *App
	run      *netRunner
	out, err *strings.Builder
}

func newNetFixture(t *testing.T) *netFixture {
	t.Helper()
	cfg, tlsDir := hostFixture(t, "internal")
	dir := filepath.Dir(cfg)
	f := &netFixture{cfg: cfg, dir: dir, run: &netRunner{}}
	f.paths = networkPaths{
		NetworkDir: filepath.Join(dir, "net"), K3sDir: filepath.Join(dir, "k3s"),
		SystemdDir: filepath.Join(dir, "systemd"), SystemCADir: filepath.Join(dir, "cacerts"),
	}
	f.app, f.out, f.err = hostApp(&recRunner{}, tlsDir)
	f.app.Runner = f.run
	f.app.ServingPoll = time.Millisecond
	f.app.ServingTimeout = time.Second
	f.app.Getenv = func(k string) string {
		switch k {
		case "GITSTACK_NETWORK_DIR":
			return f.paths.NetworkDir
		case "GITSTACK_K3S_DIR":
			return f.paths.K3sDir
		case "GITSTACK_SYSTEMD_DIR":
			return f.paths.SystemdDir
		case "GITSTACK_CA_CERTS_DIR":
			return f.paths.SystemCADir
		}
		return ""
	}
	return f
}

// fakeProxy risponde a ogni richiesta e ricorda gli URL assoluti ricevuti.
func fakeProxy(t *testing.T, status int) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.String())
		mu.Unlock()
		// Un registry risponde 401 senza credenziali e 200 con quelle giuste.
		code := status
		if code == http.StatusUnauthorized && r.Header.Get("Authorization") != "" {
			code = http.StatusOK
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func TestConfigSetNetworkApplica(t *testing.T) {
	f := newNetFixture(t)
	proxy, seen := fakeProxy(t, http.StatusUnauthorized) // un registry risponde 401 su /v2/ senza credenziali
	caFile := filepath.Join(f.dir, "corp.pem")
	if err := os.WriteFile(caFile, testCAPEM(t), 0o600); err != nil {
		t.Fatal(err)
	}
	pwFile := filepath.Join(f.dir, "pw")
	if err := os.WriteFile(pwFile, []byte("s3cr\"et\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"config", "set", "network", "--config", f.cfg,
		"--http-proxy", proxy.URL, "--no-proxy", "git.corp.test,dl.corp.test",
		"--registry-mirror", "http://registry.corp.test:5000", "--registry-username", "puller",
		"--registry-password-file", pwFile, "--download-mirror", "http://dl.corp.test:8080",
		"--ca-bundle", caFile}
	// Il mirror delle immagini è fuori da no_proxy: la prova passa dal proxy e
	// vede 200/401; il mirror dei download è in no_proxy: va diretto, quindi
	// fallisce (nessuno ascolta) e senza --force niente cambia.
	if code := f.app.Run(context.Background(), args); code != ExitConfig {
		t.Fatalf("exit %d, atteso ExitConfig: out=%s err=%s", code, f.out, f.err)
	}
	if !strings.Contains(f.err.String(), "mirror dei download") {
		t.Errorf("l'errore non nomina il mirror che non risponde: %s", f.err)
	}
	if _, err := os.Stat(f.paths.registries()); err == nil {
		t.Error("registries.yaml scritto nonostante la prova fallita")
	}
	if len(f.run.calls) != 0 {
		t.Errorf("comandi eseguiti a prova fallita: %v", f.run.names())
	}

	// Con il mirror dei download dietro il proxy (non in no_proxy) tutto risponde.
	for i, a := range args {
		if a == "git.corp.test,dl.corp.test" {
			args[i] = "git.corp.test"
		}
	}
	f.out.Reset()
	f.err.Reset()
	if code := f.app.Run(context.Background(), args); code != ExitOK {
		t.Fatalf("exit %d: out=%s err=%s", code, f.out, f.err)
	}
	joined := strings.Join(*seen, " ")
	if !strings.Contains(joined, "registry.corp.test:5000/v2/") || !strings.Contains(joined, "dl.corp.test:8080") {
		t.Errorf("la prova non è passata dal proxy: %v", *seen)
	}

	reg, err := os.ReadFile(f.paths.registries())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"*":`, `- "http://registry.corp.test:5000"`, `"registry.corp.test:5000":`,
		`username: "puller"`, `password: "s3cr\"et"`} {
		if !strings.Contains(string(reg), want) {
			t.Errorf("registries.yaml senza %q:\n%s", want, reg)
		}
	}
	if strings.Contains(string(reg), "ca_file") {
		t.Errorf("ca_file con mirror http:\n%s", reg)
	}
	if st, _ := os.Stat(f.paths.registries()); st.Mode().Perm() != 0o600 && os.PathSeparator == '/' {
		t.Errorf("registries.yaml con modo %v (porta una password)", st.Mode())
	}
	drop, err := os.ReadFile(f.paths.dropIn())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[Service]", `Environment="HTTP_PROXY=` + proxy.URL + `"`,
		`Environment="HTTPS_PROXY=` + proxy.URL + `"`, `10.42.0.0/16,10.43.0.0/16,.svc,.cluster.local,git.corp.test"`} {
		if !strings.Contains(string(drop), want) {
			t.Errorf("drop-in senza %q:\n%s", want, drop)
		}
	}
	for _, p := range []string{f.paths.caCopy(), f.paths.systemCA()} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("manca %s: %v", p, err)
		}
	}
	// Ordine: daemon-reload, restart, attesa Ready, ConfigMap, helm.
	names := f.run.names()
	idx := func(sub string) int {
		for i, n := range names {
			if strings.Contains(n, sub) {
				return i
			}
		}
		return -1
	}
	order := []string{"systemctl daemon-reload", "systemctl restart k3s", "kubectl get nodes", "kubectl -n default create configmap gitstack-corporate-ca", "kubectl apply", "helm"}
	last := -1
	for _, o := range order {
		i := idx(o)
		if i < 0 {
			t.Fatalf("manca %q in %v", o, names)
		}
		if i < last {
			t.Errorf("%q fuori ordine in %v", o, names)
		}
		last = i
	}
	helm := f.run.calls[idx("helm")]
	hj := strings.Join(helm.args, " ")
	for _, want := range []string{"--reuse-values", "proxy.http=" + proxy.URL, "proxy.https=" + proxy.URL,
		`proxy.noProxy=git.corp.test`, "corporateCA.configMap=gitstack-corporate-ca"} {
		if !strings.Contains(hj, want) {
			t.Errorf("helm senza %q: %s", want, hj)
		}
	}
	// La configurazione si rilegge e conserva il resto.
	loaded, err := config.Load(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	n := loaded.Network
	if n.HTTPProxy != proxy.URL || n.RegistryUsername != "puller" || n.RegistryPassword != `s3cr"et` ||
		n.DownloadMirror != "http://dl.corp.test:8080" || n.CABundle != f.paths.caCopy() || n.NoProxy != "git.corp.test" {
		t.Errorf("network: %+v", n)
	}
	b, _ := os.ReadFile(f.cfg)
	if !strings.Contains(string(b), "# commento") || !strings.Contains(string(b), "host: 192.168.1.20") {
		t.Errorf("config rovinata:\n%s", b)
	}
	if strings.Contains(f.out.String(), "s3cr") {
		t.Errorf("la password è nell'output: %s", f.out)
	}

	// Stessa richiesta di nuovo: niente da fare e nessun riavvio.
	before := len(f.run.calls)
	f.out.Reset()
	if code := f.app.Run(context.Background(), args); code != ExitOK {
		t.Fatalf("secondo giro: exit %d: %s", code, f.err)
	}
	if len(f.run.calls) != before || !strings.Contains(f.out.String(), "niente da fare") {
		t.Errorf("il secondo giro ha rifatto qualcosa: %v / %s", f.run.names()[before:], f.out)
	}
}

func TestConfigSetNetworkForceEClear(t *testing.T) {
	f := newNetFixture(t)
	// Proxy che non ascolta: con --force si applica, senza no.
	args := []string{"config", "set", "network", "--config", f.cfg, "--http-proxy", "http://127.0.0.1:1", "--no-proxy", "a.test,b.test"}
	if code := f.app.Run(context.Background(), args); code != ExitConfig {
		t.Fatalf("senza --force: exit %d", code)
	}
	if !strings.Contains(f.err.String(), "proxy HTTP") {
		t.Errorf("manca il proxy fra i non raggiungibili: %s", f.err)
	}
	if code := f.app.Run(context.Background(), append(args, "--force")); code != ExitOK {
		t.Fatalf("con --force: exit %d: %s", code, f.err)
	}
	helm := f.run.calls[len(f.run.calls)-1]
	if !strings.Contains(strings.Join(helm.args, " "), `proxy.noProxy=a.test\,b.test`) {
		t.Errorf("le virgole di noProxy non sono protette: %v", helm.args)
	}
	if !strings.Contains(strings.Join(helm.args, " "), "corporateCA.configMap= ") && !strings.HasSuffix(strings.Join(helm.args[:len(helm.args)-3], " "), "corporateCA.configMap=") {
		t.Errorf("corporateCA.configMap dovrebbe essere vuoto: %v", helm.args)
	}
	if _, err := os.Stat(f.paths.dropIn()); err != nil {
		t.Fatalf("drop-in mancante: %v", err)
	}

	// --clear toglie tutto: file, sezione, valori Helm.
	f.run.calls = nil
	if code := f.app.Run(context.Background(), []string{"config", "set", "network", "--config", f.cfg, "--clear", "--force"}); code != ExitOK {
		t.Fatalf("clear: exit %d: %s", code, f.err)
	}
	if _, err := os.Stat(f.paths.dropIn()); err == nil {
		t.Error("drop-in ancora presente dopo --clear")
	}
	if !f.run.has("systemctl restart k3s") {
		t.Errorf("k3s non riavviato: %v", f.run.names())
	}
	b, _ := os.ReadFile(f.cfg)
	if strings.Contains(string(b), "network:") || !strings.Contains(string(b), "namespace: default") {
		t.Errorf("config:\n%s", b)
	}
}

func TestConfigSetNetworkErrori(t *testing.T) {
	f := newNetFixture(t)
	run := func(args ...string) int {
		f.err.Reset()
		return f.app.Run(context.Background(), append([]string{"config", "set", "network", "--config", f.cfg}, args...))
	}
	if code := run(); code != ExitUsage {
		t.Errorf("senza opzioni: %d", code)
	}
	if code := run("--http-proxy", "proxy.corp:3128"); code != ExitUsage {
		t.Errorf("proxy senza schema: %d (%s)", code, f.err)
	}
	if code := run("--registry-username", "u"); code != ExitUsage {
		t.Errorf("utente senza mirror/password: %d", code)
	}
	bad := filepath.Join(f.dir, "bad.pem")
	_ = os.WriteFile(bad, []byte("non è un certificato"), 0o600)
	if code := run("--ca-bundle", bad); code != ExitUsage || !strings.Contains(f.err.String(), "nessun certificato") {
		t.Errorf("CA non valida: %d %s", code, f.err)
	}
	if len(f.run.calls) != 0 {
		t.Errorf("comandi eseguiti su input non valido: %v", f.run.names())
	}
}

func TestRenderRegistriesConCA(t *testing.T) {
	n := config.Network{RegistryMirror: "https://reg.corp:5000", CABundle: "/etc/gitstack/network/ca.crt"}
	got := renderRegistries(n, n.CABundle)
	want := "# Scritto da GitStack (install.sh o gitstack config set network): non modificarlo a mano.\n" +
		"mirrors:\n  \"*\":\n    endpoint:\n      - \"https://reg.corp:5000\"\n" +
		"configs:\n  \"reg.corp:5000\":\n    tls:\n      ca_file: \"/etc/gitstack/network/ca.crt\"\n"
	if got != want {
		t.Errorf("registries.yaml:\n%s\natteso:\n%s", got, want)
	}
	if renderRegistries(config.Network{}, "") != "" {
		t.Error("senza mirror non si scrive niente")
	}
}

func TestRenderDropInSoloHTTPEPercento(t *testing.T) {
	got := renderDropIn(config.Network{HTTPProxy: "http://u:p%40ss@proxy.corp:3128"})
	if !strings.Contains(got, `Environment="HTTPS_PROXY=http://u:p%%40ss@proxy.corp:3128"`) {
		t.Errorf("HTTPS non eredita HTTP o il percento non è raddoppiato:\n%s", got)
	}
	if renderDropIn(config.Network{}) != "" {
		t.Error("senza proxy niente drop-in")
	}
}

func TestNoProxyMatches(t *testing.T) {
	cases := []struct {
		list, host string
		want       bool
	}{
		{"registry.corp", "registry.corp", true},
		{".corp", "registry.corp", true},
		{"corp", "registry.corp", true},
		{"10.0.0.0/8", "10.1.2.3", true},
		{"other", "registry.corp", false},
		{"", "registry.corp", false},
	}
	for _, c := range cases {
		if got := noProxyMatches(c.list, c.host); got != c.want {
			t.Errorf("noProxyMatches(%q, %q) = %v", c.list, c.host, got)
		}
	}
}
