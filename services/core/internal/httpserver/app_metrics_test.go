package httpserver

import (
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// GIT-384 (H12): le medie per copia diventano una serie per passo, sommata sulle copie; un passo
// senza CPU (o senza memoria) non ha punto nella serie, mai zero.

func TestFoldMetricBuckets_SommaPerPasso(t *testing.T) {
	t0 := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Minute)
	f := func(v float64) *float64 { return &v }
	buckets := []store.AppMetricBucket{
		{Time: t0, Copy: "web-1", CPUMillis: f(10), MemoryBytes: f(100)},
		{Time: t0, Copy: "web-2", CPUMillis: f(30), MemoryBytes: nil},
		{Time: t1, Copy: "web-1", CPUMillis: nil, MemoryBytes: f(200)},
	}
	cpu, mem := foldMetricBuckets(buckets)
	if len(cpu) != 1 || !cpu[0].Time.Equal(t0) || cpu[0].Value != 40 {
		t.Fatalf("CPU: %+v (voluto un punto a t0 con 40 millicore)", cpu)
	}
	if len(mem) != 2 || mem[0].Value != 100 || !mem[1].Time.Equal(t1) || mem[1].Value != 200 {
		t.Fatalf("memoria: %+v (voluti due punti, 100 a t0 e 200 a t1)", mem)
	}
}

func TestFoldMetricBuckets_VuotoNonEZero(t *testing.T) {
	cpu, mem := foldMetricBuckets(nil)
	if cpu == nil || mem == nil || len(cpu) != 0 || len(mem) != 0 {
		t.Fatalf("senza campioni le serie sono vuote e non nil (per il JSON): %v %v", cpu, mem)
	}
}
