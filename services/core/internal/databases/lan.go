package databases

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/google/uuid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Accesso dalla LAN (GIT-554, ADR 0003 punto 6, DB16 e DB17).
//
// Ordine con la LAN accesa: il certificato con i SAN dell'istanza (ensureTLS) -> il pod pronto -> la nuova
// catena montata e ricaricata da PostgreSQL (reloadTLS) -> SOLO ALLORA Service e db-lan (openLAN). Una porta
// aperta con la foglia vecchia farebbe fallire verify-full. Spenta, Service e policy si tolgono subito
// (SetLAN) e a ogni giro, prima di qualunque altro passo (closeLAN).

// DefaultLANPortFrom e DefaultLANPortTo: intervallo proposto dall'ADR 0003 (non una misura).
const (
	DefaultLANPortFrom = 15432
	DefaultLANPortTo   = 15531
)

// DefaultReservedPorts sono le porte dell'installazione che la LAN non assegna mai: SSH dell'host, HTTP, HTTPS,
// Git SSH, API del cluster, kubelet e gateway della CI. Le altre le aggiunge Config.ReservedPorts (chart).
var DefaultReservedPorts = []int{22, 80, 443, 2222, 6443, 8443, 10250}

// ValidatePortRange verifica l'intervallo (stesse regole del contratto: 1024-65535, da <= a).
func ValidatePortRange(from, to int) error {
	if from < 1024 || to > 65535 || from > to {
		return fmt.Errorf("intervallo di porte LAN non valido %d-%d: servono porte tra 1024 e 65535 con da <= a", from, to)
	}
	return nil
}

// LANPortPolicy e' la politica di assegnazione delle porte di questa istanza.
func (c *Controller) LANPortPolicy() store.LANPortPolicy {
	from, to := c.cfg.LANPortFrom, c.cfg.LANPortTo
	if rt := c.runtime.Load(); rt != nil && rt.LANPortFrom > 0 && rt.LANPortTo > 0 {
		from, to = rt.LANPortFrom, rt.LANPortTo
	}
	if from == 0 && to == 0 {
		from, to = DefaultLANPortFrom, DefaultLANPortTo
	}
	reserved := append(append([]int(nil), DefaultReservedPorts...), c.cfg.ReservedPorts...)
	return store.LANPortPolicy{From: from, To: to, Reserved: reserved}
}

// lanLock serializza, per database, SetLAN e il passo LAN del reconciler: un giro che ha letto LANEnabled=true
// non puo' ricreare Service e policy dopo che uno spegnimento e' riuscito.
func (c *Controller) lanLock(id uuid.UUID) *sync.Mutex {
	m, _ := c.lanLocks.LoadOrStore(id, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// SetLAN accende o spegne la LAN del database. Spegnere chiude la porta prima di rispondere: se la chiusura
// fallisce ritorna l'errore (l'API risponde con un errore, mai 200) e la porta RESTA riservata nel modello;
// il retry (un altro PUT false, o il giro del reconciler) completa la chiusura e solo allora la libera.
// Accendere assegna la porta e sveglia il ciclo, che apre il Service appena il certificato e' caricato.
func (c *Controller) SetLAN(ctx context.Context, d store.ManagedDB, enable bool, actor *store.AppActor) (store.ManagedDB, error) {
	mu := c.lanLock(d.ID)
	mu.Lock()
	defer mu.Unlock()
	out, err := c.st.SetManagedDBLAN(ctx, d.ID, enable, c.LANPortPolicy(), actor, c.cfg.Now())
	if err != nil {
		return store.ManagedDB{}, err
	}
	if !enable {
		released, err := c.closeAndRelease(ctx, out)
		if err != nil {
			return out, fmt.Errorf("%w: %v", ErrLANCloseFailed, err)
		}
		out = released
	}
	c.Kick()
	return out, nil
}

// ErrLANCloseFailed: la LAN e' spenta nel modello ma Service o policy non sono stati rimossi; la porta
// resta riservata e la chiusura si ritenta.
var ErrLANCloseFailed = errors.New("accesso LAN non chiuso: Service o NetworkPolicy ancora presenti, si ritenta")

// closeAndRelease toglie Service e policy e, solo a chiusura riuscita, libera la porta. Chiamarlo con la
// serratura LAN del database.
func (c *Controller) closeAndRelease(ctx context.Context, d store.ManagedDB) (store.ManagedDB, error) {
	if err := c.closeLAN(ctx, d); err != nil {
		return d, err
	}
	if d.LANPort == nil {
		return d, nil
	}
	return c.st.ReleaseManagedDBLANPort(ctx, d.ID)
}

// syncLAN porta la LAN allo stato scritto OGGI nel modello (non a quello dello snapshot del giro): spenta
// chiude e libera la porta, accesa e con il certificato caricato apre. Sotto la serratura LAN.
func (c *Controller) syncLAN(ctx context.Context, snapshot store.ManagedDB, tlsReady bool) error {
	mu := c.lanLock(snapshot.ID)
	mu.Lock()
	defer mu.Unlock()
	d, err := c.st.GetManagedDBByID(ctx, snapshot.ID)
	if errors.Is(err, store.ErrManagedDBNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !d.LANEnabled {
		_, err := c.closeAndRelease(ctx, d)
		return err
	}
	if !tlsReady {
		return nil
	}
	// tlsReady viene dallo snapshot del giro: il certificato puo' essere stato emesso quando la LAN era ancora
	// spenta (solo nome interno) e il PUT true arrivato dopo. Con lo stato di OGGI si verifica che la catena
	// CARICATA da PostgreSQL abbia i SAN richiesti; altrimenti non si apre e il giro successivo (che parte da
	// uno snapshot acceso) emette, monta e ricarica la foglia giusta.
	ok, err := c.servedTLSMatches(ctx, d)
	if err != nil {
		return err
	}
	if !ok {
		c.Kick()
		return nil
	}
	return c.openLAN(ctx, d)
}

// servedTLSMatches dice se il Secret pg-tls ha i SAN voluti dallo stato corrente di d e se PostgreSQL ha
// ricaricato proprio quella catena.
func (c *Controller) servedTLSMatches(ctx context.Context, d store.ManagedDB) (bool, error) {
	dns, ips, err := c.serverSANs(d)
	if err != nil {
		return false, err
	}
	sec, err := c.kube.CoreV1().Secrets(Namespace(d.ID)).Get(ctx, SecretTLS, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	have := sec.Annotations[annotationSANs]
	if have == "" {
		have = sanKey([]string{ServiceHost(d.ID)}, nil)
	}
	if have != sanKey(dns, ips) {
		return false, nil
	}
	c.mu.Lock()
	loaded := c.tlsLoaded[d.ID]
	c.mu.Unlock()
	return loaded == chainHash(sec.Data["tls.crt"]), nil
}

// LANInfo e' cio' che il portale mostra con la LAN accesa.
type LANInfo struct {
	Host     string
	ChainPEM string
	// Open: Service e policy esistono e PostgreSQL ha caricato il certificato.
	Open bool
}

// LANInfo risolve l'indirizzo (requestHost se e' uno dei nomi o IP dell'istanza, se no il primo) e la catena
// pubblica del server. Con la LAN spenta ritorna il valore zero.
func (c *Controller) LANInfo(ctx context.Context, d store.ManagedDB, requestHost string) (LANInfo, error) {
	if !d.LANEnabled || d.LANPort == nil {
		return LANInfo{}, nil
	}
	dns, ips, err := LANNames(c.cfg.CADir)
	if err != nil {
		return LANInfo{}, err
	}
	info := LANInfo{Host: pickLANHost(requestHost, dns, ips)}
	sec, err := c.kube.CoreV1().Secrets(Namespace(d.ID)).Get(ctx, SecretTLS, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return LANInfo{}, err
	}
	if err == nil {
		info.ChainPEM = string(sec.Data["tls.crt"])
		c.mu.Lock()
		info.Open = c.tlsLoaded[d.ID] == chainHash(sec.Data["tls.crt"])
		c.mu.Unlock()
	}
	if info.Open {
		_, err := c.kube.CoreV1().Services(Namespace(d.ID)).Get(ctx, LANServiceName, metav1.GetOptions{})
		info.Open = err == nil
	}
	return info, nil
}

func pickLANHost(requestHost string, dns []string, ips []net.IP) string {
	h := strings.ToLower(strings.TrimSpace(requestHost))
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.Trim(h, "[]")
	for _, n := range dns {
		if h == strings.ToLower(n) {
			return h
		}
	}
	for _, ip := range ips {
		if parsed := net.ParseIP(h); parsed != nil && parsed.Equal(ip) {
			return ip.String()
		}
	}
	if len(dns) > 0 {
		return dns[0]
	}
	if len(ips) > 0 {
		return ips[0].String()
	}
	return ""
}

// ufwCommand e' il comando suggerito: GitStack non tocca ufw (DB18).
func UFWCommand(port int) string {
	return fmt.Sprintf("ufw allow from <rete> to any port %d proto tcp", port)
}

// serverSANs sono i SAN voluti della foglia: il nome interno e, con la LAN accesa, i nomi e gli IP dell'istanza.
func (c *Controller) serverSANs(d store.ManagedDB) ([]string, []net.IP, error) {
	dns := []string{ServiceHost(d.ID)}
	if !d.LANEnabled {
		return dns, nil, nil
	}
	lanDNS, lanIPs, err := LANNames(c.cfg.CADir)
	if err != nil {
		return nil, nil, err
	}
	return append(dns, lanDNS...), lanIPs, nil
}

func chainHash(pem []byte) string {
	sum := sha256.Sum256(pem)
	return hex.EncodeToString(sum[:8])
}

// tlsMounted dice se il file che PostgreSQL legge e' gia' la catena del Secret: il kubelet aggiorna i volumi
// dei Secret con un ritardo (fino a ~1 minuto), e un pg_reload_conf prima di allora ricarica il file vecchio.
func (c *Controller) tlsMounted(ctx context.Context, d store.ManagedDB, want []byte) bool {
	if c.cfg.Exec == nil {
		return true
	}
	out, err := c.cfg.Exec.Exec(ctx, Namespace(d.ID), PodName, WorkloadName, []string{"cat", "/etc/gitstack/pg-tls/tls.crt"}, nil)
	return err == nil && strings.TrimSpace(out) == strings.TrimSpace(string(want))
}

// reloadTLS fa ricaricare a PostgreSQL la catena del Secret appena e' montata. Ritorna true quando la catena
// corrente e' quella caricata (anche dopo un riavvio di core: un reload in piu' non fa danni).
func (c *Controller) reloadTLS(ctx context.Context, d store.ManagedDB) (bool, error) {
	sec, err := c.kube.CoreV1().Secrets(Namespace(d.ID)).Get(ctx, SecretTLS, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	want := chainHash(sec.Data["tls.crt"])
	c.mu.Lock()
	loaded := c.tlsLoaded[d.ID]
	c.mu.Unlock()
	if loaded == want {
		return true, nil
	}
	if !c.tlsMounted(ctx, d, sec.Data["tls.crt"]) {
		return false, nil
	}
	if c.sql != nil {
		if err := c.sql.Exec(ctx, d, "postgres", `SELECT pg_reload_conf()`); err != nil {
			return false, err
		}
	}
	c.mu.Lock()
	c.tlsLoaded[d.ID] = want
	c.mu.Unlock()
	return true, nil
}

// closeLAN toglie Service e NetworkPolicy della LAN: la porta smette di rispondere.
func (c *Controller) closeLAN(ctx context.Context, d store.ManagedDB) error {
	ns := Namespace(d.ID)
	if err := c.kube.CoreV1().Services(ns).Delete(ctx, LANServiceName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	if err := c.kube.NetworkingV1().NetworkPolicies(ns).Delete(ctx, LANNetworkPolicyName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// openLAN crea o riallinea Service e db-lan sulla porta assegnata. Prima la policy: il Service non
// riceve traffico che db-isolation scarterebbe.
func (c *Controller) openLAN(ctx context.Context, d store.ManagedDB) error {
	if d.LANPort == nil {
		return nil
	}
	ns := Namespace(d.ID)
	if err := c.ensureNetworkPolicy(ctx, ns, BuildLANNetworkPolicy(d, c.cfg.PodCIDR)); err != nil {
		return err
	}
	want := BuildLANService(d, *d.LANPort)
	services := c.kube.CoreV1().Services(ns)
	cur, err := services.Get(ctx, LANServiceName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = services.Create(ctx, want, metav1.CreateOptions{})
		return ignoreExists(err)
	}
	if err != nil {
		return err
	}
	if len(cur.Spec.Ports) == 1 && cur.Spec.Ports[0].Port == want.Spec.Ports[0].Port && cur.Spec.Type == want.Spec.Type {
		return nil
	}
	cur.Spec.Type, cur.Spec.Ports = want.Spec.Type, want.Spec.Ports
	_, err = services.Update(ctx, cur, metav1.UpdateOptions{})
	return err
}
