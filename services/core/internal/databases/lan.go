package databases

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"strings"

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
	if from == 0 && to == 0 {
		from, to = DefaultLANPortFrom, DefaultLANPortTo
	}
	reserved := append(append([]int(nil), DefaultReservedPorts...), c.cfg.ReservedPorts...)
	return store.LANPortPolicy{From: from, To: to, Reserved: reserved}
}

// SetLAN accende o spegne la LAN del database. Spegnere chiude la porta prima di rispondere; accendere
// assegna la porta e sveglia il ciclo, che apre il Service appena il certificato e' caricato.
func (c *Controller) SetLAN(ctx context.Context, d store.ManagedDB, enable bool, actor *store.AppActor) (store.ManagedDB, error) {
	out, err := c.st.SetManagedDBLAN(ctx, d.ID, enable, c.LANPortPolicy(), actor, c.cfg.Now())
	if err != nil {
		return store.ManagedDB{}, err
	}
	if !enable {
		if err := c.closeLAN(ctx, out); err != nil {
			return out, err
		}
	}
	c.Kick()
	return out, nil
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
