package apps

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// GIT-384 (H12): le quantità di metrics-server diventano millicore e byte; una sorgente che non
// risponde è un errore, non zero.

func TestParsePodMetrics_SommaDeiContainer(t *testing.T) {
	obj := map[string]any{
		"metadata": map[string]any{"name": "web-1"},
		"containers": []any{
			map[string]any{"name": "app", "usage": map[string]any{"cpu": "3m", "memory": "20Mi"}},
			map[string]any{"name": "sidecar", "usage": map[string]any{"cpu": "250m", "memory": "1Gi"}},
		},
	}
	u, err := parsePodMetrics(obj)
	if err != nil {
		t.Fatal(err)
	}
	if u.CPUMillis != 253 {
		t.Errorf("CPU = %d millicore (voluti 253)", u.CPUMillis)
	}
	if want := int64(20<<20) + int64(1<<30); u.MemoryBytes != want {
		t.Errorf("memoria = %d byte (voluti %d)", u.MemoryBytes, want)
	}
}

func TestParsePodMetrics_QuantitaNonValidaErrore(t *testing.T) {
	obj := map[string]any{"containers": []any{
		map[string]any{"usage": map[string]any{"cpu": "tre", "memory": "20Mi"}},
	}}
	if _, err := parsePodMetrics(obj); err == nil {
		t.Fatal("una CPU non leggibile deve essere un errore, non zero")
	}
}

func TestReadPodMetrics_SorgenteFintaERisposta(t *testing.T) {
	id := uuid.New()
	want := map[string]PodUsage{"web-1": {CPUMillis: 12, MemoryBytes: 1024}}
	c := New(nil, nil, nil, Config{PodMetrics: func(_ context.Context, ns string) (map[string]PodUsage, error) {
		if ns != Namespace(id) {
			t.Errorf("namespace = %s", ns)
		}
		return want, nil
	}})
	got, err := c.readPodMetrics(context.Background(), Namespace(id))
	if err != nil || got["web-1"] != want["web-1"] {
		t.Fatalf("letture: %v %v", got, err)
	}

	boom := errors.New("metrics-server non risponde")
	c = New(nil, nil, nil, Config{PodMetrics: func(context.Context, string) (map[string]PodUsage, error) { return nil, boom }})
	if _, err := c.readPodMetrics(context.Background(), "ns"); !errors.Is(err, boom) {
		t.Fatalf("errore della sorgente non propagato: %v", err)
	}
}

func TestReadPodMetrics_SenzaSorgenteErrore(t *testing.T) {
	c := New(nil, nil, nil, Config{})
	if _, err := c.readPodMetrics(context.Background(), "ns"); err == nil {
		t.Fatal("senza metrics-server la lettura risponde errore: l'API dirà non disponibile")
	}
}
