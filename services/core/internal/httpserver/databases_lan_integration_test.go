//go:build integration

package httpserver_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/fathorMB/GitStack/services/core/internal/databases"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// writeLANCA scrive in una cartella un'intermedia come quella dell'installer (name constraints su
// *.svc.cluster.local e sui nomi dell'istanza).
func writeLANCA(t *testing.T) string {
	t.Helper()
	rootKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	rootTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test Root"}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(10 * 365 * 24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTmpl, rootTmpl, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := x509.ParseCertificate(rootDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "Test Database CA"}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(5 * 365 * 24 * time.Hour), IsCA: true, BasicConstraintsValid: true, MaxPathLen: 0, MaxPathLenZero: true,
		KeyUsage: x509.KeyUsageCertSign, PermittedDNSDomainsCritical: true, PermittedDNSDomains: []string{"svc.cluster.local", "gitstack.test"},
		PermittedIPRanges: []*net.IPNet{{IP: net.IPv4(192, 0, 2, 10).To4(), Mask: net.CIDRMask(32, 32)}}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, root, &key.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	kd, _ := x509.MarshalPKCS8PrivateKey(key)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tls.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tls.key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd}), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func newLANEnv(t *testing.T) *dbEnv {
	t.Helper()
	kube := fake.NewSimpleClientset()
	sql := &sqlSpy{}
	proxy := &lateController{}
	holder := &dbEnv{kube: kube, sql: sql, proxy: proxy}
	holder.appsEnv = newAppsEnv(t, httpserver.WithDatabases(httpserver.DatabasesConfig{Controller: proxy}))
	holder.ctl = databases.New(store.New(holder.pool), kube, databases.Config{SQL: sql, Images: map[int]databases.Image{}, CADir: writeLANCA(t),
		LANPortFrom: 15432, LANPortTo: 15433})
	proxy.set(holder.ctl)
	return holder
}

func (e *dbEnv) lan(method, path, user, body string, want int, code string) openapi.DatabaseLan {
	e.t.Helper()
	rec := e.do(method, path, user, body)
	e.want(rec, want, code)
	var out openapi.DatabaseLan
	if want == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			e.t.Fatalf("%v: %s", err, rec.Body.String())
		}
	}
	return out
}

// DB2 e DB16: accendere e spegnere la LAN richiede `admin` sull'owner (chi non lo e' vede 404); accesa da'
// porta, indirizzo e comando ufw; mai password nella risposta; spenta torna a null.
func TestDatabaseLAN_ApiPermessiEStato(t *testing.T) {
	e := newLANEnv(t)
	e.create("alice", "alice", `{"name":"shop","pgMajor":17}`, http.StatusAccepted)
	const path = "/databases/alice/shop/lan"

	off := e.lan(http.MethodGet, path, "alice", "", http.StatusOK, "")
	if off.Enabled || off.Port != nil || off.Host != nil || off.CaCertificate != nil || off.UfwCommand != nil {
		t.Fatalf("LAN spenta di default: %+v", off)
	}
	// Chi non e' admin dell'owner: 404 su lettura e scrittura, e la LAN resta spenta.
	e.want(e.do(http.MethodGet, path, "bob", ""), http.StatusNotFound, "not_found")
	e.want(e.do(http.MethodPut, path, "bob", `{"enabled":true}`), http.StatusNotFound, "not_found")
	if got := e.lan(http.MethodGet, path, "alice", "", http.StatusOK, ""); got.Enabled {
		t.Fatal("un estraneo ha acceso la LAN")
	}
	// Senza `enabled` 422, con campi sconosciuti 400.
	e.want(e.do(http.MethodPut, path, "alice", `{}`), http.StatusUnprocessableEntity, "validation_failed")
	e.want(e.do(http.MethodPut, path, "alice", `{"enabled":true,"port":22}`), http.StatusBadRequest, "")

	on := e.lan(http.MethodPut, path, "alice", `{"enabled":true}`, http.StatusOK, "")
	if !on.Enabled || on.Port == nil || *on.Port != 15432 || on.Host == nil || *on.Host != "gitstack.test" ||
		on.UfwCommand == nil || *on.UfwCommand != "ufw allow from <rete> to any port 15432 proto tcp" {
		t.Fatalf("LAN accesa: %+v", on)
	}
	rec := e.do(http.MethodPut, path, "alice", `{"enabled":true}`)
	if strings.Contains(strings.ToLower(rec.Body.String()), "password") {
		t.Fatalf("la risposta LAN non deve contenere password: %s", rec.Body.String())
	}
	// L'API del database mostra lo stato e la porta.
	var db openapi.Database
	rec = e.do(http.MethodGet, "/databases/alice/shop", "alice", "")
	e.want(rec, http.StatusOK, "")
	_ = json.Unmarshal(rec.Body.Bytes(), &db)
	if !db.LanEnabled || db.LanPort == nil || *db.LanPort != 15432 {
		t.Fatalf("database: %+v", db)
	}
	// L'amministratore dell'istanza puo' leggere e spegnere.
	e.id.sysAdmin[users["carol"]] = true
	if got := e.lan(http.MethodGet, path, "carol", "", http.StatusOK, ""); !got.Enabled {
		t.Fatalf("admin: %+v", got)
	}
	// Spenta: tutto torna a null e la porta e' libera.
	offAgain := e.lan(http.MethodPut, path, "alice", `{"enabled":false}`, http.StatusOK, "")
	if offAgain.Enabled || offAgain.Port != nil || offAgain.Host != nil || offAgain.UfwCommand != nil {
		t.Fatalf("LAN spenta: %+v", offAgain)
	}
	// Spegnere due volte e' innocuo.
	e.lan(http.MethodPut, path, "alice", `{"enabled":false}`, http.StatusOK, "")
}

// DB16: porte distinte fra database, 409 port_unavailable a intervallo finito, e la porta liberata si riusa.
func TestDatabaseLAN_ApiPorteEConflitto(t *testing.T) {
	e := newLANEnv(t) // intervallo 15432-15433
	for _, n := range []string{"uno", "due", "tre"} {
		e.create("alice", "alice", `{"name":"`+n+`","pgMajor":17}`, http.StatusAccepted)
	}
	p1 := e.lan(http.MethodPut, "/databases/alice/uno/lan", "alice", `{"enabled":true}`, http.StatusOK, "")
	p2 := e.lan(http.MethodPut, "/databases/alice/due/lan", "alice", `{"enabled":true}`, http.StatusOK, "")
	if *p1.Port == *p2.Port {
		t.Fatalf("due database con la stessa porta: %d", *p1.Port)
	}
	e.lan(http.MethodPut, "/databases/alice/tre/lan", "alice", `{"enabled":true}`, http.StatusConflict, "port_unavailable")
	if got := e.lan(http.MethodGet, "/databases/alice/tre/lan", "alice", "", http.StatusOK, ""); got.Enabled {
		t.Fatal("un 409 non lascia la LAN accesa")
	}
	e.lan(http.MethodPut, "/databases/alice/uno/lan", "alice", `{"enabled":false}`, http.StatusOK, "")
	p3 := e.lan(http.MethodPut, "/databases/alice/tre/lan", "alice", `{"enabled":true}`, http.StatusOK, "")
	if *p3.Port != *p1.Port {
		t.Fatalf("la porta liberata va riusata: %d, attesa %d", *p3.Port, *p1.Port)
	}
}

// Con la funzione spenta (nessun controller) la LAN risponde 422 databases_disabled come il resto.
func TestDatabaseLAN_ApiFunzioneSpenta(t *testing.T) {
	e := newAppsEnv(t)
	e.want(e.do(http.MethodGet, "/databases/alice/shop/lan", "alice", ""), http.StatusUnprocessableEntity, "databases_disabled")
	e.want(e.do(http.MethodPut, "/databases/alice/shop/lan", "alice", `{"enabled":true}`), http.StatusUnprocessableEntity, "databases_disabled")
}

// Rework 1: se Kubernetes non riesce a togliere il Service, PUT enabled=false NON risponde 200 (503
// lan_close_failed), l'API non mostra la porta come libera per altri database, e il retry riesce.
func TestDatabaseLAN_ApiChiusuraFallita(t *testing.T) {
	e := newLANEnv(t) // intervallo 15432-15433
	e.create("alice", "alice", `{"name":"uno","pgMajor":17}`, http.StatusAccepted)
	e.create("alice", "alice", `{"name":"due","pgMajor":17}`, http.StatusAccepted)
	e.create("alice", "alice", `{"name":"tre","pgMajor":17}`, http.StatusAccepted)
	on := e.lan(http.MethodPut, "/databases/alice/uno/lan", "alice", `{"enabled":true}`, http.StatusOK, "")
	var fail atomic.Bool
	e.kube.PrependReactor("delete", "services", func(k8stesting.Action) (bool, runtime.Object, error) {
		if fail.Load() {
			return true, nil, errors.New("apiserver non raggiungibile")
		}
		return false, nil, nil
	})
	fail.Store(true)
	e.want(e.do(http.MethodPut, "/databases/alice/uno/lan", "alice", `{"enabled":false}`), http.StatusServiceUnavailable, "lan_close_failed")
	// L'API mostra la LAN spenta (porta null), ma la porta e' ancora riservata: due prende l'altra, tre trova l'intervallo finito.
	var db openapi.Database
	rec := e.do(http.MethodGet, "/databases/alice/uno", "alice", "")
	e.want(rec, http.StatusOK, "")
	_ = json.Unmarshal(rec.Body.Bytes(), &db)
	if db.LanEnabled || db.LanPort != nil {
		t.Fatalf("database dopo la chiusura fallita: %+v", db)
	}
	due := e.lan(http.MethodPut, "/databases/alice/due/lan", "alice", `{"enabled":true}`, http.StatusOK, "")
	if *due.Port == *on.Port {
		t.Fatalf("la porta %d e' stata riassegnata con il Service ancora aperto", *on.Port)
	}
	e.lan(http.MethodPut, "/databases/alice/tre/lan", "alice", `{"enabled":true}`, http.StatusConflict, "port_unavailable")
	// Retry riuscito.
	fail.Store(false)
	off := e.lan(http.MethodPut, "/databases/alice/uno/lan", "alice", `{"enabled":false}`, http.StatusOK, "")
	if off.Enabled || off.Port != nil {
		t.Fatalf("retry: %+v", off)
	}
	if tre := e.lan(http.MethodPut, "/databases/alice/tre/lan", "alice", `{"enabled":true}`, http.StatusOK, ""); *tre.Port != *on.Port {
		t.Fatalf("porta liberata dal retry: %d, attesa %d", *tre.Port, *on.Port)
	}
}
