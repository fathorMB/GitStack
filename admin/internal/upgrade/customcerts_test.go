package upgrade

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/fathorMB/GitStack/admin/internal/config"
)

// certsKube simula i namespace per la migrazione dei certificati del cliente (GIT-494):
// secrets["ns/nome"] e tlsstores["ns"] = {default, refs}.
type certsKube struct {
	mu       sync.Mutex
	nsExists bool
	secrets  map[string]bool
	store    map[string][2]string // namespace -> {defaultCertificate, certificates separati da spazio}
	calls    []string
}

func (k *certsKube) Run(_ context.Context, _ []string, name string, args ...string) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if name != "k3s" || len(args) == 0 || args[0] != "kubectl" {
		return nil, errors.New("comando inatteso")
	}
	a := args[1:]
	j := strings.Join(a, " ")
	k.calls = append(k.calls, j)
	notFound := errors.New("NotFound")
	switch {
	case j == "get namespace gitstack-certs -o name":
		if k.nsExists {
			return []byte("namespace/gitstack-certs\n"), nil
		}
		return nil, notFound
	case len(a) >= 6 && a[0] == "-n" && a[2] == "get" && a[3] == "secret" && a[4] == "-l":
		var out string
		for key := range k.secrets {
			if ns, n, _ := strings.Cut(key, "/"); ns == a[1] {
				out += "secret/" + n + "\n"
			}
		}
		return []byte(out), nil
	case len(a) >= 6 && a[0] == "-n" && a[2] == "get" && a[3] == "secret":
		if k.secrets[a[1]+"/"+a[4]] {
			return []byte("secret/" + a[4] + "\n"), nil
		}
		return nil, notFound
	case len(a) >= 5 && a[2] == "delete" && a[3] == "secret":
		delete(k.secrets, a[1]+"/"+a[4])
		return []byte("deleted"), nil
	case len(a) >= 6 && a[2] == "get" && a[3] == "tlsstores.traefik.io":
		st, ok := k.store[a[1]]
		if !ok {
			return nil, notFound
		}
		switch {
		case a[len(a)-1] == "name":
			return []byte("tlsstore.traefik.io/default"), nil
		case strings.Contains(a[len(a)-1], "defaultCertificate"):
			return []byte(st[0]), nil
		default:
			return []byte(st[1]), nil
		}
	case len(a) >= 5 && a[2] == "delete" && a[3] == "tlsstores.traefik.io":
		delete(k.store, a[1])
		return []byte("deleted"), nil
	}
	return nil, errors.New("kubectl inatteso: " + j)
}

func newCertsOpts(k *certsKube) *Options {
	return &Options{Cfg: &config.Config{Release: "gitstack", Namespace: "default", Kubeconfig: "/k"}, Runner: k}
}

func TestMigrateCustomCerts_TogliSoloCioCheCoreHaRicreato(t *testing.T) {
	for _, old := range []string{"default", "kube-system"} {
		t.Run(old, func(t *testing.T) {
			k := &certsKube{nsExists: true,
				secrets: map[string]bool{old + "/gitstack-domain-a": true, old + "/gitstack-domain-b": true, "gitstack-certs/gitstack-domain-a": true},
				store:   map[string][2]string{"gitstack-certs": {"", "gitstack-domain-a"}},
			}
			if old == "kube-system" {
				k.store["kube-system"] = [2]string{"", "gitstack-domain-a gitstack-domain-b"}
			}
			res := &Result{}
			newCertsOpts(k).migrateCustomCerts(context.Background(), res)
			if k.secrets[old+"/gitstack-domain-a"] {
				t.Errorf("la copia vecchia di a (già nel namespace nuovo) doveva essere tolta")
			}
			if !k.secrets[old+"/gitstack-domain-b"] {
				t.Errorf("b non è ancora nel namespace nuovo: non va cancellato")
			}
			if !k.secrets["gitstack-certs/gitstack-domain-a"] {
				t.Errorf("il Secret nuovo non va toccato")
			}
			if !strings.Contains(strings.Join(res.Summary, "\n"), old+"/gitstack-domain-b") {
				t.Errorf("il riepilogo deve dire che b resta: %v", res.Summary)
			}
			if _, still := k.store["kube-system"]; still && old == "kube-system" {
				t.Errorf("il TLSStore di kube-system creato da core doveva essere tolto")
			}
		})
	}
}

func TestMigrateCustomCerts_NonToccaTLSStoreAltrui(t *testing.T) {
	k := &certsKube{nsExists: true,
		secrets: map[string]bool{},
		store: map[string][2]string{
			"gitstack-certs": {"", "gitstack-domain-a"},
			"kube-system":    {"gitstack-tls", "gitstack-domain-a"}, // ha un defaultCertificate: non è di core
		},
	}
	newCertsOpts(k).migrateCustomCerts(context.Background(), &Result{})
	if _, ok := k.store["kube-system"]; !ok {
		t.Fatal("un TLSStore con defaultCertificate non è di core: non va cancellato")
	}
	k.store["kube-system"] = [2]string{"", "altro-secret"}
	newCertsOpts(k).migrateCustomCerts(context.Background(), &Result{})
	if _, ok := k.store["kube-system"]; !ok {
		t.Fatal("un TLSStore con certificati non nostri non va cancellato")
	}
}

func TestMigrateCustomCerts_SenzaNamespaceNuovoNonFaNiente(t *testing.T) {
	k := &certsKube{nsExists: false, secrets: map[string]bool{"default/gitstack-domain-a": true}}
	newCertsOpts(k).migrateCustomCerts(context.Background(), &Result{})
	if !k.secrets["default/gitstack-domain-a"] {
		t.Fatal("senza il namespace dei certificati non si cancella niente")
	}
	if len(k.calls) != 1 {
		t.Fatalf("attesa una sola chiamata, trovate: %v", k.calls)
	}
}

func TestMigrateCustomCerts_ReleaseGitstackAppNonFaNiente(t *testing.T) {
	k := &certsKube{nsExists: true, secrets: map[string]bool{"default/gitstack-domain-a": true}}
	o := newCertsOpts(k)
	o.Cfg.Release = "gitstack-app-x"
	o.migrateCustomCerts(context.Background(), &Result{})
	if len(k.calls) != 0 || !k.secrets["default/gitstack-domain-a"] {
		t.Fatalf("il namespace gitstack-app-* è il perimetro delle app: nessuna chiamata, trovate %v", k.calls)
	}
}
