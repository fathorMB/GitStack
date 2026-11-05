//go:build integration

package databases

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

func lanEnv(t *testing.T) *ctlEnv {
	return newCtlEnv(t, func(c *Config) {
		c.LANPortFrom, c.LANPortTo = 15432, 15435
		c.ReservedPorts = []int{15433} // la porta di un altro servizio dell'installazione
	})
}

func (e *ctlEnv) running(owner, name string) store.ManagedDB {
	e.t.Helper()
	d := e.create(owner, name, 17)
	ctx := context.Background()
	e.ctl.Reconcile(ctx, e.reload(d.ID))
	e.markReady(d)
	e.ctl.Reconcile(ctx, e.reload(d.ID))
	if got := e.reload(d.ID); got.Status != store.DBRunning {
		e.t.Fatalf("stato %s (%v), voluto running", got.Status, got.StatusReason)
	}
	return e.reload(d.ID)
}

func (e *ctlEnv) lanObjects(d store.ManagedDB) (svc *corev1.Service, policyExists bool) {
	e.t.Helper()
	ns := Namespace(d.ID)
	s, err := e.kube.CoreV1().Services(ns).Get(context.Background(), LANServiceName, metav1.GetOptions{})
	if err == nil {
		svc = s
	} else if !apierrors.IsNotFound(err) {
		e.t.Fatal(err)
	}
	_, err = e.kube.NetworkingV1().NetworkPolicies(ns).Get(context.Background(), LANNetworkPolicyName, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		e.t.Fatal(err)
	}
	return svc, err == nil
}

// DB16: spenta di default, nessun Service ne' db-lan e nessuna porta; il certificato ha il solo nome interno.
func TestDatabaseLAN_SpentaDiDefault(t *testing.T) {
	e := lanEnv(t)
	d := e.running("alice", "shop")
	if d.LANEnabled || d.LANPort != nil {
		t.Fatalf("LAN accesa alla creazione: %+v", d)
	}
	if svc, pol := e.lanObjects(d); svc != nil || pol {
		t.Fatalf("oggetti LAN con la LAN spenta: svc=%v policy=%v", svc, pol)
	}
	tls, _ := e.kube.CoreV1().Secrets(Namespace(d.ID)).Get(context.Background(), SecretTLS, metav1.GetOptions{})
	chain := parseChain(t, tls.Data["tls.crt"])
	if len(chain[0].DNSNames) != 1 || chain[0].DNSNames[0] != ServiceHost(d.ID) || len(chain[0].IPAddresses) != 0 {
		t.Fatalf("SAN della foglia con la LAN spenta: %v %v", chain[0].DNSNames, chain[0].IPAddresses)
	}
}

func parseChain(t *testing.T, pemBytes []byte) []*x509.Certificate {
	t.Helper()
	var out []*x509.Certificate
	for {
		var b *pem.Block
		if b, pemBytes = pem.Decode(pemBytes); b == nil {
			return out
		}
		c, err := x509.ParseCertificate(b.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
}

// DB16/DB17: accesa, la porta e' dell'intervallo e non e' riservata; la foglia viene riemessa con i SAN
// dell'istanza e ricaricata da PostgreSQL PRIMA di aprire il Service; db-lan e Service nascono insieme.
func TestDatabaseLAN_AccesaApreServiceECertificato(t *testing.T) {
	e := lanEnv(t)
	ctx := context.Background()
	d := e.running("alice", "shop")
	got, err := e.ctl.SetLAN(ctx, d, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !got.LANEnabled || got.LANPort == nil || *got.LANPort != 15432 {
		t.Fatalf("porta assegnata: %+v", got)
	}
	// Prima del giro del reconciler non c'e' ancora nulla di aperto.
	if svc, _ := e.lanObjects(d); svc != nil {
		t.Fatal("il Service non si apre prima che il certificato sia caricato")
	}
	reloadsBefore := strings.Count(strings.Join(e.sql.stmts, "\n"), "pg_reload_conf")
	e.ctl.Reconcile(ctx, e.reload(d.ID))
	svc, pol := e.lanObjects(d)
	if svc == nil || !pol {
		t.Fatalf("Service e db-lan devono esistere: svc=%v policy=%v", svc, pol)
	}
	if svc.Spec.Type != corev1.ServiceTypeLoadBalancer || svc.Spec.Ports[0].Port != 15432 || svc.Spec.Ports[0].TargetPort.IntValue() != 5432 {
		t.Fatalf("Service: %+v", svc.Spec)
	}
	if n := strings.Count(strings.Join(e.sql.stmts, "\n"), "pg_reload_conf"); n <= reloadsBefore {
		t.Fatal("il certificato nuovo va fatto ricaricare a PostgreSQL prima di aprire la porta")
	}
	tls, _ := e.kube.CoreV1().Secrets(Namespace(d.ID)).Get(ctx, SecretTLS, metav1.GetOptions{})
	chain := parseChain(t, tls.Data["tls.crt"])
	if len(chain) != 2 {
		t.Fatalf("catena foglia+intermedia: %d", len(chain))
	}
	have := strings.Join(chain[0].DNSNames, ",")
	if !strings.Contains(have, "gitstack.test") || !strings.Contains(have, ServiceHost(d.ID)) || len(chain[0].IPAddresses) != 1 || chain[0].IPAddresses[0].String() != "192.0.2.10" {
		t.Fatalf("SAN con la LAN accesa: %v %v", chain[0].DNSNames, chain[0].IPAddresses)
	}
	// Idempotente: un altro giro non cambia ne' la porta ne' il certificato.
	before := tls.Data["tls.crt"]
	e.ctl.Reconcile(ctx, e.reload(d.ID))
	again, _ := e.kube.CoreV1().Secrets(Namespace(d.ID)).Get(ctx, SecretTLS, metav1.GetOptions{})
	if string(again.Data["tls.crt"]) != string(before) {
		t.Fatal("la foglia non va riemessa a ogni giro")
	}
	// Il certificato e le informazioni per il portale.
	info, err := e.ctl.LANInfo(ctx, e.reload(d.ID), "gitstack.test:443")
	if err != nil || info.Host != "gitstack.test" || !info.Open || !strings.Contains(info.ChainPEM, "BEGIN CERTIFICATE") {
		t.Fatalf("LANInfo: %+v %v", info, err)
	}
}

// DB16: spegnere chiude SUBITO (prima di qualunque giro), toglie la porta dal modello, rimette il certificato
// col solo nome interno; db-isolation resta.
func TestDatabaseLAN_SpegnereChiudeSubito(t *testing.T) {
	e := lanEnv(t)
	ctx := context.Background()
	d := e.running("alice", "shop")
	if _, err := e.ctl.SetLAN(ctx, d, true, nil); err != nil {
		t.Fatal(err)
	}
	e.ctl.Reconcile(ctx, e.reload(d.ID))
	if svc, pol := e.lanObjects(d); svc == nil || !pol {
		t.Fatal("la LAN doveva essere aperta")
	}
	got, err := e.ctl.SetLAN(ctx, e.reload(d.ID), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.LANEnabled || got.LANPort != nil {
		t.Fatalf("modello dopo lo spegnimento: %+v", got)
	}
	// Nessun Reconcile in mezzo: la porta e' gia' chiusa.
	if svc, pol := e.lanObjects(d); svc != nil || pol {
		t.Fatalf("dopo lo spegnimento restano: svc=%v policy=%v", svc, pol)
	}
	if _, err := e.kube.NetworkingV1().NetworkPolicies(Namespace(d.ID)).Get(ctx, NetworkPolicyName, metav1.GetOptions{}); err != nil {
		t.Fatalf("db-isolation deve restare: %v", err)
	}
	// Un giro dopo: il certificato torna col solo nome interno e nulla si riapre.
	e.ctl.Reconcile(ctx, e.reload(d.ID))
	tls, _ := e.kube.CoreV1().Secrets(Namespace(d.ID)).Get(ctx, SecretTLS, metav1.GetOptions{})
	if c := parseChain(t, tls.Data["tls.crt"]); len(c[0].DNSNames) != 1 || len(c[0].IPAddresses) != 0 {
		t.Fatalf("SAN dopo lo spegnimento: %v %v", c[0].DNSNames, c[0].IPAddresses)
	}
	if svc, pol := e.lanObjects(d); svc != nil || pol {
		t.Fatal("la LAN si e' riaperta da sola")
	}
	if info, err := e.ctl.LANInfo(ctx, e.reload(d.ID), ""); err != nil || info.Host != "" || info.Open {
		t.Fatalf("LANInfo a LAN spenta: %+v %v", info, err)
	}
}

// Anche se qualcuno ricrea Service e policy a mano con la LAN spenta, il giro successivo li toglie, e lo fa
// anche col pod non pronto.
func TestDatabaseLAN_SpentaIlCicloRimuoveOgniResiduo(t *testing.T) {
	e := lanEnv(t)
	ctx := context.Background()
	d := e.running("alice", "shop")
	ns := Namespace(d.ID)
	if _, err := e.kube.CoreV1().Services(ns).Create(ctx, BuildLANService(d, 15432), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.kube.NetworkingV1().NetworkPolicies(ns).Create(ctx, BuildLANNetworkPolicy(d, ""), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	e.ctl.Reconcile(ctx, e.reload(d.ID))
	if svc, pol := e.lanObjects(d); svc != nil || pol {
		t.Fatalf("residui con la LAN spenta: svc=%v policy=%v", svc, pol)
	}
}

// DB16: due database con la LAN accesa non si scontrano; una porta riservata non si assegna; a intervallo
// finito 409 (ErrLANPortUnavailable); spegnendo la porta si libera e si riassegna.
func TestDatabaseLAN_PorteDistinteERiservate(t *testing.T) {
	e := lanEnv(t) // intervallo 15432-15435, 15433 riservata: assegnabili 15432, 15434, 15435
	ctx := context.Background()
	a := e.create("alice", "a", 17)
	b := e.create("alice", "b", 17)
	c := e.create("bob", "c", 17)
	f := e.create("bob", "f", 17)
	ports := map[string]int{}
	for name, d := range map[string]store.ManagedDB{"a": a, "b": b, "c": c} {
		got, err := e.ctl.SetLAN(ctx, d, true, nil)
		if err != nil {
			t.Fatal(err)
		}
		ports[name] = *got.LANPort
	}
	seen := map[int]string{}
	for name, p := range ports {
		if p == 15433 {
			t.Fatalf("assegnata la porta riservata a %s", name)
		}
		if p < 15432 || p > 15435 {
			t.Fatalf("%s fuori intervallo: %d", name, p)
		}
		if other, dup := seen[p]; dup {
			t.Fatalf("%s e %s hanno la stessa porta %d", name, other, p)
		}
		seen[p] = name
	}
	if _, err := e.ctl.SetLAN(ctx, f, true, nil); !errors.Is(err, store.ErrLANPortUnavailable) {
		t.Fatalf("intervallo finito: %v", err)
	}
	if got := e.reload(f.ID); got.LANEnabled || got.LANPort != nil {
		t.Fatalf("un rifiuto non lascia la LAN accesa: %+v", got)
	}
	// Idempotente: riaccendere non cambia porta.
	again, err := e.ctl.SetLAN(ctx, e.reload(a.ID), true, nil)
	if err != nil || *again.LANPort != ports["a"] {
		t.Fatalf("riaccensione: %+v %v", again, err)
	}
	// Spenta una, la sua porta si libera per f.
	if _, err := e.ctl.SetLAN(ctx, e.reload(b.ID), false, nil); err != nil {
		t.Fatal(err)
	}
	got, err := e.ctl.SetLAN(ctx, f, true, nil)
	if err != nil || *got.LANPort != ports["b"] {
		t.Fatalf("la porta liberata va a f: %+v %v (b aveva %d)", got, err, ports["b"])
	}
	// L'amministratore vede quali porte sono in uso e di chi.
	list, err := e.st.ListManagedDBLANPorts(ctx)
	if err != nil || len(list) != 3 {
		t.Fatalf("porte in uso: %+v %v", list, err)
	}
	for i := 1; i < len(list); i++ {
		if list[i-1].Port >= list[i].Port {
			t.Fatalf("elenco non ordinato: %+v", list)
		}
	}
}

// DB16: la porta resta la stessa dopo un riavvio di core (controller nuovo sullo stesso database) e dopo
// un upgrade (certificato riemesso, immagine fissata): Service e porta non cambiano.
func TestDatabaseLAN_PortaStabileDopoRiavvioEUpgrade(t *testing.T) {
	e := lanEnv(t)
	ctx := context.Background()
	if _, err := e.ctl.SetLAN(ctx, e.create("zed", "z", 17), true, nil); err != nil { // occupa 15432
		t.Fatal(err)
	}
	d := e.running("alice", "shop")
	got, err := e.ctl.SetLAN(ctx, d, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	port := *got.LANPort
	e.ctl.Reconcile(ctx, e.reload(d.ID))
	if svc, _ := e.lanObjects(d); svc == nil || svc.Spec.Ports[0].Port != int32(port) {
		t.Fatalf("Service: %+v", svc)
	}
	// «Riavvio»: un controller nuovo (memoria vuota) sullo stesso modello e sullo stesso cluster.
	restarted := New(e.st, e.kube, e.ctl.cfg)
	restarted.Reconcile(ctx, e.reload(d.ID))
	if again := e.reload(d.ID); again.LANPort == nil || *again.LANPort != port {
		t.Fatalf("porta dopo il riavvio: %+v", again.LANPort)
	}
	if svc, pol := e.lanObjects(d); svc == nil || !pol || svc.Spec.Ports[0].Port != int32(port) {
		t.Fatalf("Service dopo il riavvio: %+v %v", svc, pol)
	}
	// «Upgrade»: il certificato scade tra meno di 30 giorni e viene riemesso (RenewBefore), con l'orologio avanti.
	later := restarted.cfg
	later.Now = func() time.Time { return time.Now().UTC().Add(400 * 24 * time.Hour) }
	upgraded := New(e.st, e.kube, later)
	upgraded.Reconcile(ctx, e.reload(d.ID))
	if again := e.reload(d.ID); again.LANPort == nil || *again.LANPort != port {
		t.Fatalf("porta dopo l'upgrade: %+v", again.LANPort)
	}
	if svc, _ := e.lanObjects(d); svc == nil || svc.Spec.Ports[0].Port != int32(port) {
		t.Fatalf("Service dopo l'upgrade: %+v", svc)
	}
	// Una Service con la porta sbagliata (modificata a mano) viene riallineata.
	svc, _ := e.lanObjects(d)
	svc.Spec.Ports[0].Port = 31999
	if _, err := e.kube.CoreV1().Services(Namespace(d.ID)).Update(ctx, svc, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	restarted.Reconcile(ctx, e.reload(d.ID))
	if svc, _ := e.lanObjects(d); svc.Spec.Ports[0].Port != int32(port) {
		t.Fatalf("porta non riallineata: %d", svc.Spec.Ports[0].Port)
	}
}

// Il Service non si apre finche' il kubelet non ha montato la catena nuova: PostgreSQL ricaricherebbe il
// file vecchio e verify-full fallirebbe. Il pg_reload_conf parte solo a montaggio avvenuto.
func TestDatabaseLAN_NonSiApreFinoAlMontaggioDelCertificato(t *testing.T) {
	var e *ctlEnv
	mounted := false
	e = newCtlEnv(t, func(c *Config) {
		c.LANPortFrom, c.LANPortTo = 15432, 15435
		c.Exec = execFunc(func(ctx context.Context, ns string, cmd []string) (string, error) {
			if len(cmd) != 2 || cmd[0] != "cat" || cmd[1] != "/etc/gitstack/pg-tls/tls.crt" {
				t.Errorf("comando inatteso nel pod: %v", cmd)
			}
			if !mounted {
				return "catena-vecchia", nil
			}
			sec, err := e.kube.CoreV1().Secrets(ns).Get(ctx, SecretTLS, metav1.GetOptions{})
			if err != nil {
				return "", err
			}
			return string(sec.Data["tls.crt"]), nil
		})
	})
	ctx := context.Background()
	d := e.running("alice", "shop")
	if _, err := e.ctl.SetLAN(ctx, d, true, nil); err != nil {
		t.Fatal(err)
	}
	reloads := func() int { return strings.Count(strings.Join(e.sql.stmts, "\n"), "pg_reload_conf") }
	before := reloads()
	e.ctl.Reconcile(ctx, e.reload(d.ID))
	if svc, pol := e.lanObjects(d); svc != nil || pol {
		t.Fatalf("aperto con la catena non ancora montata: svc=%v policy=%v", svc, pol)
	}
	if reloads() != before {
		t.Fatal("pg_reload_conf prima del montaggio ricaricherebbe il file vecchio")
	}
	if info, _ := e.ctl.LANInfo(ctx, e.reload(d.ID), ""); info.Open {
		t.Fatal("LANInfo dice aperta una LAN non aperta")
	}
	mounted = true
	e.ctl.Reconcile(ctx, e.reload(d.ID))
	if svc, pol := e.lanObjects(d); svc == nil || !pol {
		t.Fatalf("montata la catena la LAN si apre: svc=%v policy=%v", svc, pol)
	}
	if reloads() <= before {
		t.Fatal("dopo il montaggio PostgreSQL va fatto ricaricare")
	}
}

type execFunc func(ctx context.Context, ns string, cmd []string) (string, error)

func (f execFunc) Exec(ctx context.Context, ns, _, _ string, cmd []string, _ io.Reader) (string, error) {
	return f(ctx, ns, cmd)
}

// failDeleteServices fa fallire ogni Delete di Service finche' *fail e' vero (Kubernetes che non risponde).
func failDeleteServices(e *ctlEnv, fail *atomic.Bool) {
	e.kube.PrependReactor("delete", "services", func(k8stesting.Action) (bool, runtime.Object, error) {
		if fail.Load() {
			return true, nil, errors.New("apiserver non raggiungibile")
		}
		return false, nil, nil
	})
}

// Rework 1: se la chiusura fallisce lo spegnimento NON riesce (ErrLANCloseFailed), la porta resta riservata
// (un altro database non la riceve mentre il vecchio Service esiste) e il retry completa la chiusura.
func TestDatabaseLAN_ChiusuraFallitaTienePortaERiprova(t *testing.T) {
	e := lanEnv(t) // assegnabili 15432, 15434, 15435
	ctx := context.Background()
	var fail atomic.Bool
	failDeleteServices(e, &fail)
	a := e.running("alice", "a")
	b := e.create("alice", "b", 17)
	got, err := e.ctl.SetLAN(ctx, a, true, nil)
	if err != nil || *got.LANPort != 15432 {
		t.Fatalf("accensione: %+v %v", got, err)
	}
	e.ctl.Reconcile(ctx, e.reload(a.ID))
	if svc, _ := e.lanObjects(a); svc == nil {
		t.Fatal("la LAN doveva essere aperta")
	}

	fail.Store(true)
	out, err := e.ctl.SetLAN(ctx, e.reload(a.ID), false, nil)
	if !errors.Is(err, ErrLANCloseFailed) {
		t.Fatalf("la chiusura fallita deve essere un errore, non un successo: %v", err)
	}
	if out.LANEnabled || out.LANPort == nil || *out.LANPort != 15432 {
		t.Fatalf("modello dopo la chiusura fallita: enabled=%v port=%v (la porta resta riservata)", out.LANEnabled, out.LANPort)
	}
	if svc, _ := e.lanObjects(a); svc == nil {
		t.Fatal("il Service esiste ancora: la porta e' aperta")
	}
	// La porta non e' riassegnabile mentre il Service esiste.
	nb, err := e.ctl.SetLAN(ctx, b, true, nil)
	if err != nil || *nb.LANPort == 15432 {
		t.Fatalf("la porta 15432 e' stata riassegnata a b con il Service ancora aperto: %+v %v", nb.LANPort, err)
	}
	// Il giro del reconciler, con Kubernetes ancora giu', non libera la porta ne' riapre.
	e.ctl.Reconcile(ctx, e.reload(a.ID))
	if again := e.reload(a.ID); again.LANPort == nil || again.LANEnabled {
		t.Fatalf("modello dopo un giro con Kubernetes giu': %+v", again)
	}
	// Retry riuscito (secondo PUT false): Service e policy spariti, porta libera, riassegnabile.
	fail.Store(false)
	out, err = e.ctl.SetLAN(ctx, e.reload(a.ID), false, nil)
	if err != nil || out.LANPort != nil || out.LANEnabled {
		t.Fatalf("retry: %+v %v", out, err)
	}
	if svc, pol := e.lanObjects(a); svc != nil || pol {
		t.Fatalf("dopo il retry restano: svc=%v policy=%v", svc, pol)
	}
	c := e.create("bob", "c", 17)
	if nc, err := e.ctl.SetLAN(ctx, c, true, nil); err != nil || *nc.LANPort != 15432 {
		t.Fatalf("la porta liberata va riassegnata: %+v %v", nc.LANPort, err)
	}
}

// Rework 1: anche senza un secondo PUT, il giro del reconciler completa la chiusura e libera la porta.
func TestDatabaseLAN_ChiusuraFallitaLaCompletaIlReconciler(t *testing.T) {
	e := lanEnv(t)
	ctx := context.Background()
	var fail atomic.Bool
	failDeleteServices(e, &fail)
	a := e.running("alice", "a")
	if _, err := e.ctl.SetLAN(ctx, a, true, nil); err != nil {
		t.Fatal(err)
	}
	e.ctl.Reconcile(ctx, e.reload(a.ID))
	fail.Store(true)
	if _, err := e.ctl.SetLAN(ctx, e.reload(a.ID), false, nil); !errors.Is(err, ErrLANCloseFailed) {
		t.Fatal(err)
	}
	fail.Store(false)
	e.ctl.Reconcile(ctx, e.reload(a.ID))
	if got := e.reload(a.ID); got.LANPort != nil || got.LANEnabled {
		t.Fatalf("il reconciler doveva completare la chiusura: %+v", got)
	}
	if svc, pol := e.lanObjects(a); svc != nil || pol {
		t.Fatalf("restano: svc=%v policy=%v", svc, pol)
	}
}

// Riaccendere prima che la chiusura sia completata riusa la stessa porta.
func TestDatabaseLAN_RiaccensioneDuranteChiusuraRiusaLaPorta(t *testing.T) {
	e := lanEnv(t)
	ctx := context.Background()
	var fail atomic.Bool
	failDeleteServices(e, &fail)
	a := e.running("alice", "a")
	on, _ := e.ctl.SetLAN(ctx, a, true, nil)
	e.ctl.Reconcile(ctx, e.reload(a.ID))
	fail.Store(true)
	_, _ = e.ctl.SetLAN(ctx, e.reload(a.ID), false, nil)
	fail.Store(false)
	again, err := e.ctl.SetLAN(ctx, e.reload(a.ID), true, nil)
	if err != nil || !again.LANEnabled || *again.LANPort != *on.LANPort {
		t.Fatalf("riaccensione: %+v %v", again, err)
	}
}

// Rework 1, interleaving 1: un giro con lo snapshot LANEnabled=true che parte DOPO un PUT false riuscito non
// ricrea Service ne' policy.
func TestDatabaseLAN_GiroConSnapshotVecchioNonRiapre(t *testing.T) {
	e := lanEnv(t)
	ctx := context.Background()
	a := e.running("alice", "a")
	if _, err := e.ctl.SetLAN(ctx, a, true, nil); err != nil {
		t.Fatal(err)
	}
	stale := e.reload(a.ID) // LANEnabled=true
	e.ctl.Reconcile(ctx, stale)
	if _, err := e.ctl.SetLAN(ctx, e.reload(a.ID), false, nil); err != nil {
		t.Fatal(err)
	}
	e.ctl.Reconcile(ctx, stale)
	if svc, pol := e.lanObjects(a); svc != nil || pol {
		t.Fatalf("un giro con snapshot vecchio ha riaperto la LAN: svc=%v policy=%v", svc, pol)
	}
	if got := e.reload(a.ID); got.LANEnabled || got.LANPort != nil {
		t.Fatalf("modello: %+v", got)
	}
}

// Rework 1, interleaving 2 (deterministico): il giro e' DENTRO openLAN (bloccato sulla Create del Service)
// quando arriva il PUT false; il PUT aspetta la serratura, e quando il giro finisce chiude tutto.
func TestDatabaseLAN_SpegnimentoAspettaIlGiroInCorso(t *testing.T) {
	e := lanEnv(t)
	ctx := context.Background()
	a := e.running("alice", "a")
	if _, err := e.ctl.SetLAN(ctx, a, true, nil); err != nil {
		t.Fatal(err)
	}
	inCreate, release := make(chan struct{}), make(chan struct{})
	e.kube.PrependReactor("create", "services", func(act k8stesting.Action) (bool, runtime.Object, error) {
		if act.(k8stesting.CreateAction).GetObject().(*corev1.Service).Name == LANServiceName {
			close(inCreate)
			<-release
		}
		return false, nil, nil
	})
	reconciled := make(chan struct{})
	go func() { e.ctl.Reconcile(ctx, e.reload(a.ID)); close(reconciled) }()
	<-inCreate
	disabled := make(chan error, 1)
	go func() {
		_, err := e.ctl.SetLAN(ctx, e.reload(a.ID), false, nil)
		disabled <- err
	}()
	select {
	case err := <-disabled:
		t.Fatalf("lo spegnimento non puo' finire mentre il giro apre la LAN (%v)", err)
	case <-time.After(300 * time.Millisecond):
	}
	if got := e.reload(a.ID); !got.LANEnabled {
		t.Fatal("il modello non va toccato prima di avere la serratura")
	}
	close(release)
	<-reconciled
	if err := <-disabled; err != nil {
		t.Fatal(err)
	}
	if svc, pol := e.lanObjects(a); svc != nil || pol {
		t.Fatalf("dopo lo spegnimento restano: svc=%v policy=%v", svc, pol)
	}
	if got := e.reload(a.ID); got.LANEnabled || got.LANPort != nil {
		t.Fatalf("modello: %+v", got)
	}
}

// Rework 2, interleaving opposto: il giro parte con lo snapshot SPENTO (foglia col solo nome interno), il PUT true
// arriva mentre il giro e' a meta', e il giro vecchio arriva a syncLAN: non apre finche' la foglia con i SAN LAN
// non e' emessa, montata e ricaricata. Il giro successivo apre, e verify-full verifica il nome LAN.
func TestDatabaseLAN_GiroVecchioSpentoNonApreConFogliaSenzaSanLAN(t *testing.T) {
	e := lanEnv(t)
	ctx := context.Background()
	d := e.running("alice", "shop") // spenta, foglia col solo nome interno gia' caricata
	stale := e.reload(d.ID)
	if stale.LANEnabled {
		t.Fatal("lo snapshot deve essere spento")
	}
	var armed atomic.Bool
	armed.Store(true)
	atPods, release := make(chan struct{}), make(chan struct{})
	// Il giro legge il pod dopo ensureTLS e prima del passo LAN: li' si ferma.
	e.kube.PrependReactor("get", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		if armed.CompareAndSwap(true, false) {
			close(atPods)
			<-release
		}
		return false, nil, nil
	})
	done := make(chan struct{})
	go func() { e.ctl.Reconcile(ctx, stale); close(done) }()
	<-atPods
	if _, err := e.ctl.SetLAN(ctx, e.reload(d.ID), true, nil); err != nil {
		t.Fatal(err)
	}
	close(release)
	<-done
	if svc, pol := e.lanObjects(d); svc != nil || pol {
		t.Fatalf("il giro vecchio ha aperto con una foglia senza SAN LAN: svc=%v policy=%v", svc, pol)
	}
	tls, _ := e.kube.CoreV1().Secrets(Namespace(d.ID)).Get(ctx, SecretTLS, metav1.GetOptions{})
	if c := parseChain(t, tls.Data["tls.crt"]); len(c[0].IPAddresses) != 0 {
		t.Fatalf("la foglia non doveva avere i SAN LAN prima del giro nuovo: %v", c[0].IPAddresses)
	}
	// Giro successivo, da uno snapshot acceso: emette, ricarica, apre.
	e.ctl.Reconcile(ctx, e.reload(d.ID))
	svc, pol := e.lanObjects(d)
	if svc == nil || !pol {
		t.Fatalf("il giro successivo doveva aprire: svc=%v policy=%v", svc, pol)
	}
	tls, _ = e.kube.CoreV1().Secrets(Namespace(d.ID)).Get(ctx, SecretTLS, metav1.GetOptions{})
	chain := parseChain(t, tls.Data["tls.crt"])
	inter, roots := x509.NewCertPool(), x509.NewCertPool()
	inter.AddCert(chain[1])
	roots.AddCert(e.root)
	if _, err := chain[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter, DNSName: "gitstack.test"}); err != nil {
		t.Fatalf("verify-full sul nome LAN con la foglia servita: %v", err)
	}
}
