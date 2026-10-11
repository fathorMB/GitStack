package mirrors

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/pkg/egress"
	"github.com/fathorMB/GitStack/pkg/events/gitpush"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
)

func TestBackoff_CrescenteConTetto(t *testing.T) {
	want := []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour, 3 * time.Hour, 6 * time.Hour, 6 * time.Hour, 6 * time.Hour}
	for i, w := range want {
		if got := Backoff(i + 1); got != w {
			t.Errorf("Backoff(%d) = %s, voluto %s", i+1, got, w)
		}
	}
	if Backoff(0) != 30*time.Second || Backoff(1000) != 6*time.Hour {
		t.Error("estremi")
	}
}

func TestRelevant(t *testing.T) {
	ref := func(name string, isDefault bool, after string) gitpush.RefPush {
		return gitpush.RefPush{Ref: name, After: after, IsDefaultBranch: isDefault}
	}
	sha := strings.Repeat("a", 40)
	for _, c := range []struct {
		name string
		refs []gitpush.RefPush
		want bool
	}{
		{"branch principale (flag)", []gitpush.RefPush{ref("refs/heads/main", true, sha)}, true},
		{"branch principale (nome)", []gitpush.RefPush{ref("refs/heads/trunk", false, sha)}, true},
		{"tag", []gitpush.RefPush{ref("refs/tags/v1", false, sha)}, true},
		{"altro branch", []gitpush.RefPush{ref("refs/heads/feature", false, sha)}, false},
		{"tag cancellato", []gitpush.RefPush{ref("refs/tags/v1", false, gitpush.ZeroSHA)}, false},
		{"principale cancellato", []gitpush.RefPush{ref("refs/heads/trunk", true, gitpush.ZeroSHA)}, false},
		{"misto", []gitpush.RefPush{ref("refs/heads/feature", false, sha), ref("refs/tags/v2", false, sha)}, true},
		{"vuoto", nil, false},
	} {
		p := gitpush.Payload{Repo: gitpush.Repo{DefaultBranch: "trunk"}, Refs: c.refs}
		if got := Relevant(p); got != c.want {
			t.Errorf("%s: %v, voluto %v", c.name, got, c.want)
		}
	}
}

func TestParseURL(t *testing.T) {
	for _, ok := range []string{"https://github.com/o/r.git", "https://git.example.com:8443/a/b.git", "https://[2606:4700::1]/x.git"} {
		if _, err := ParseURL(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "http://github.com/o/r.git", "ssh://git@github.com/o/r.git", "git://github.com/o/r.git", "file:///etc/passwd",
		"https://user:pw@github.com/o/r.git", "https://github.com/o/r.git?x=1", "https://github.com/o/r.git#f", "https:///x", "--upload-pack=x",
		"https://github.com/ o", "https://" + strings.Repeat("a", 2050) + ".com"} {
		if _, err := ParseURL(bad); !errors.Is(err, ErrURLNotAllowed) {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
}

func resolver(m map[string][]string) func(context.Context, string) ([]netip.Addr, error) {
	return func(_ context.Context, host string) ([]netip.Addr, error) {
		ips, ok := m[host]
		if !ok {
			return nil, errors.New("NXDOMAIN")
		}
		var out []netip.Addr
		for _, s := range ips {
			out = append(out, netip.MustParseAddr(s))
		}
		return out, nil
	}
}

func TestEgress_PolicyDiDefault(t *testing.T) {
	e, err := NewEgress(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.Resolve = resolver(map[string][]string{
		"ok.test":       {"93.184.216.34"},
		"dual.test":     {"2606:2800:220:1::1", "93.184.216.34"},
		"rebind.test":   {"93.184.216.34", "127.0.0.1"},
		"cluster.test":  {"10.43.1.1"},
		"metadata.test": {"169.254.169.254"},
		"cgnat.test":    {"100.64.0.5"},
		"lan.test":      {"192.168.1.20"},
	})
	pick := func(raw string) (netip.Addr, error) {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return e.Pick(context.Background(), u)
	}
	if ip, err := pick("https://ok.test/a.git"); err != nil || ip.String() != "93.184.216.34" {
		t.Errorf("ok: %v %v", ip, err)
	}
	// Preferisce IPv4 quando il nome ha anche IPv6.
	if ip, err := pick("https://dual.test/a.git"); err != nil || !ip.Is4() {
		t.Errorf("dual: %v %v", ip, err)
	}
	// LAN privata fuori dal cluster: ammessa di default (come per i webhook, C8).
	if _, err := pick("https://lan.test/a.git"); err != nil {
		t.Errorf("lan: %v", err)
	}
	// Ogni IP è controllato: un nome che risponde anche con un indirizzo vietato è bloccato (rebinding).
	for _, host := range []string{"rebind.test", "cluster.test", "metadata.test", "cgnat.test"} {
		if _, err := pick("https://" + host + "/a.git"); !egress.IsBlocked(err) {
			t.Errorf("%s: err = %v, voluto un blocco egress", host, err)
		}
	}
	// IP letterali.
	for raw, blocked := range map[string]bool{"https://127.0.0.1/a.git": true, "https://[::1]/a.git": true, "https://10.42.0.1/a.git": true,
		"https://169.254.169.254/a.git": true, "https://93.184.216.34/a.git": false} {
		_, err := pick(raw)
		if egress.IsBlocked(err) != blocked {
			t.Errorf("%s: err = %v", raw, err)
		}
	}
	// Un errore di DNS non è un blocco (si ritenta).
	if _, err := pick("https://nxdomain.test/a.git"); err == nil || egress.IsBlocked(err) {
		t.Errorf("DNS: %v", err)
	}
	// Alla creazione: solo ciò che si vede dal testo.
	for raw, ok := range map[string]bool{"https://github.com/o/r.git": true, "https://localhost/a.git": false, "https://x.localhost/a.git": false,
		"https://127.0.0.1/a.git": false, "https://10.43.0.1/a.git": false, "http://github.com/o/r.git": false, "https://u:p@github.com/a.git": false} {
		err := e.CheckURL(raw)
		if (err == nil) != ok {
			t.Errorf("CheckURL(%s) = %v", raw, err)
		}
		if err != nil && !errors.Is(err, ErrURLNotAllowed) {
			t.Errorf("CheckURL(%s): errore non ErrURLNotAllowed: %v", raw, err)
		}
	}
}

func TestEgress_AllowEDenyDellAmministratore(t *testing.T) {
	// Allow per nome: restringe ai soli nomi elencati; un IP in allow sblocca gli intervalli speciali.
	e, err := NewEgress([]string{"github.com", "*.git.example.org", "100.64.0.0/10"}, []string{"evil.github.com", "203.0.113.0/24"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.Resolve = resolver(map[string][]string{
		"github.com":         {"140.82.112.3"},
		"x.git.example.org":  {"93.184.216.34"},
		"git.example.org":    {"93.184.216.34"},
		"other.com":          {"93.184.216.35"},
		"evil.github.com":    {"140.82.112.4"},
		"cgnat.git.test":     {"100.64.0.7"},
		"denied-ip.test":     {"203.0.113.9"},
		"rebind.github.com":  {"140.82.112.3", "10.42.0.9"},
		"nameallow.test":     {"100.64.0.8"},
		"fromallowlist.test": {"100.64.0.9"},
	})
	pick := func(host string) error {
		_, err := e.Pick(context.Background(), &url.URL{Scheme: "https", Host: host})
		return err
	}
	for host, ok := range map[string]bool{
		"github.com":        true,  // nome in allow
		"x.git.example.org": true,  // suffisso in allow
		"git.example.org":   false, // il suffisso *. non include l'apice
		"other.com":         false, // allow restringe: nome non elencato
		"evil.github.com":   false, // deny vince
		"cgnat.git.test":    true,  // IP in allow (100.64.0.0/10)
		"denied-ip.test":    false, // IP in deny
		"rebind.github.com": false, // nome ammesso ma IP interno: mai
	} {
		err := pick(host)
		if (err == nil) != ok {
			t.Errorf("%s: err = %v, ammesso atteso %v", host, err, ok)
		}
		if err != nil && !egress.IsBlocked(err) && host != "git.example.org" {
			t.Errorf("%s: non è un blocco: %v", host, err)
		}
	}
	// evil.github.com si rifiuta già alla creazione (nome vietato).
	if err := e.CheckURL("https://evil.github.com/a.git"); !errors.Is(err, ErrURLNotAllowed) {
		t.Errorf("CheckURL deny: %v", err)
	}
	if err := e.CheckURL("https://github.com/a.git"); err != nil {
		t.Errorf("CheckURL allow: %v", err)
	}
}

func TestMirrorPushInput_NonStampaIlToken(t *testing.T) {
	in := gitclient.MirrorPushInput{URL: "https://github.com/o/r.git", Username: "u", Token: "ghp_SEGRETISSIMO", IP: "1.2.3.4", DefaultBranch: "main"}
	for _, s := range []string{fmt.Sprint(in), fmt.Sprintf("%v", in), fmt.Sprintf("%+v", in), fmt.Sprintf("%#v", &in)} {
		if strings.Contains(s, "ghp_SEGRETISSIMO") && !strings.HasPrefix(s, "&gitclient") {
			t.Errorf("il token è nel testo: %s", s)
		}
	}
}

func TestRedactEClip(t *testing.T) {
	if got := redact("fatal: bad ghp_X credential ghp_X", "ghp_X"); strings.Contains(got, "ghp_X") {
		t.Errorf("redact: %s", got)
	}
	if got := clip(strings.Repeat("è", 800)); len(got) > 1000 || strings.ContainsRune(got, '�') {
		t.Errorf("clip: %d byte", len(got))
	}
	if got := clip("a\x00b\xffc"); strings.ContainsRune(got, 0) {
		t.Errorf("clip NUL: %q", got)
	}
}
