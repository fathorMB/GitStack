//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// DB21: senza una sorgente di CPU/RAM (metrics-server non letto da core in GIT-551, GIT-568) le metriche
// dichiarano l'assenza: available=false e serie vuote, non zeri, anche quando la dimensione e' nota. Mai
// available=true solo perche' esiste un uso del disco.
func TestDatabases_MetricheDichiaranoAssenzaDiCpuERam(t *testing.T) {
	e := newDBEnv(t)
	created := e.create("alice", "alice", `{"name":"shop","pgMajor":17}`, http.StatusAccepted)
	get := func(q string) openapi.DatabaseMetrics {
		rec := e.do(http.MethodGet, "/databases/alice/shop/metrics"+q, "alice", "")
		e.want(rec, http.StatusOK, "")
		var m openapi.DatabaseMetrics
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	m := get("")
	if m.Available || len(m.Cpu.Points) != 0 || len(m.Memory.Points) != 0 || m.Usage != nil {
		t.Fatalf("senza misure: %+v", m)
	}
	st := store.New(e.pool)
	if _, err := st.SetManagedDBUsage(context.Background(), uuid.UUID(created.Database.Id), 900<<20, store.DiskStateFor(900<<20, 1<<30)); err != nil {
		t.Fatal(err)
	}
	m = get("?range=6h")
	if m.Available || len(m.Cpu.Points) != 0 || len(m.Memory.Points) != 0 {
		t.Fatalf("la dimensione sola non rende disponibili CPU e RAM: %+v", m)
	}
	if m.Usage == nil || m.Usage.DiskState != "warning" || len(m.DatabaseBytes.Points) != 1 {
		t.Fatalf("uso del disco: %+v", m)
	}
	e.want(e.do(http.MethodGet, "/databases/alice/shop/metrics?range=9h", "alice", ""), http.StatusBadRequest, "")
	e.want(e.do(http.MethodGet, "/databases/alice/shop/metrics", "bob", ""), http.StatusNotFound, "not_found")
	// Log: il pod non c'e' nel cluster finto -> errore dichiarato, non un 500.
	if rec := e.do(http.MethodGet, "/databases/alice/shop/logs", "alice", ""); rec.Code != http.StatusUnprocessableEntity && rec.Code != http.StatusOK {
		t.Fatalf("log: %d %s", rec.Code, rec.Body.String())
	}
}
