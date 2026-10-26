package httpserver

import (
	"net/http"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Metriche delle app (v3-a, GIT-384, H12): GET /repos/{owner}/{repo}/apps/{app}/metrics. Serve `read`
// sul repo (H5). CPU e memoria sono la media per passo, sommata sulle copie (o di una sola se `copy`).
// Senza un campione con CPU nell'intervallo la risposta è available=false con serie vuote, non zeri.
// Richieste ed errori 5xx non sono raccolti in questa versione (Traefik): restano null.

// appMetricRange è la finestra e il passo di una scelta di `range`.
type appMetricRange struct {
	span time.Duration
	step int // secondi
}

var appMetricRanges = map[string]appMetricRange{
	"1h":  {time.Hour, 60},
	"6h":  {6 * time.Hour, 300},
	"24h": {24 * time.Hour, 900},
	"7d":  {7 * 24 * time.Hour, 3600},
}

// appCopyFreshness: una copia senza campioni da questo tempo non è più una copia dell'app (sostituita o
// spenta), e non compare nell'elenco delle copie.
const appCopyFreshness = 5 * time.Minute

func (s *apiServer) GetAppMetrics(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, app openapi.AppParam, params openapi.GetAppMetricsParams) {
	repo, _, ok := s.appRepo(w, r, owner, name, "read", false)
	if !ok {
		return
	}
	a, ok := s.appFor(w, r, repo, app)
	if !ok {
		return
	}
	rng := "1h"
	if params.Range != nil {
		rng = string(*params.Range)
	}
	spec, ok := appMetricRanges[rng]
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "range: 1h, 6h, 24h o 7d.")
		return
	}
	copyName := ""
	if params.Copy != nil {
		copyName = *params.Copy
	}
	ctx := r.Context()
	now := time.Now().UTC()
	since := now.Add(-spec.span)
	available, err := s.resources.AppMetricsSourceOK(ctx, a.ID, since)
	if err != nil {
		appInternalError(w, "lettura delle metriche dell'app", err)
		return
	}
	copies, err := s.resources.AppCopyStates(ctx, a.ID, now.Add(-appCopyFreshness))
	if err != nil {
		appInternalError(w, "lettura delle copie dell'app", err)
		return
	}
	restarts, err := s.resources.AppRestartsSince(ctx, a.ID, now.Add(-24*time.Hour))
	if err != nil {
		appInternalError(w, "lettura dei riavvii dell'app", err)
		return
	}
	cpu := openapi.AppMetricSeries{Unit: openapi.Millicores, Points: []openapi.AppMetricPoint{}}
	mem := openapi.AppMetricSeries{Unit: openapi.Bytes, Points: []openapi.AppMetricPoint{}}
	if available {
		buckets, err := s.resources.AppMetricBuckets(ctx, a.ID, since, spec.step, copyName)
		if err != nil {
			appInternalError(w, "lettura delle serie dell'app", err)
			return
		}
		cpu.Points, mem.Points = foldMetricBuckets(buckets)
	}
	out := openapi.AppMetrics{
		Available: available, Range: openapi.AppMetricsRange(rng), StepSeconds: spec.step,
		Cpu: cpu, Memory: mem, Restarts24h: int(restarts), Copies: []openapi.AppCopyStatus{},
	}
	for _, c := range copies {
		out.Copies = append(out.Copies, openapi.AppCopyStatus{Name: c.Copy, Ready: c.Ready, Restarts: c.Restarts})
	}
	writeJSON(w, http.StatusOK, out)
}

// foldMetricBuckets somma le medie delle copie per passo e dà le due serie (CPU e memoria) in ordine
// di tempo. Un passo senza CPU (o senza memoria) non ha punto in quella serie.
func foldMetricBuckets(buckets []store.AppMetricBucket) (cpu, mem []openapi.AppMetricPoint) {
	type acc struct {
		cpu, mem       float64
		hasCPU, hasMem bool
	}
	var order []time.Time
	byTime := map[time.Time]*acc{}
	for _, b := range buckets {
		x, seen := byTime[b.Time]
		if !seen {
			x = &acc{}
			byTime[b.Time] = x
			order = append(order, b.Time)
		}
		if b.CPUMillis != nil {
			x.cpu += *b.CPUMillis
			x.hasCPU = true
		}
		if b.MemoryBytes != nil {
			x.mem += *b.MemoryBytes
			x.hasMem = true
		}
	}
	cpu, mem = []openapi.AppMetricPoint{}, []openapi.AppMetricPoint{}
	for _, t := range order {
		x := byTime[t]
		if x.hasCPU {
			cpu = append(cpu, openapi.AppMetricPoint{Time: t, Value: float32(x.cpu)})
		}
		if x.hasMem {
			mem = append(mem, openapi.AppMetricPoint{Time: t, Value: float32(x.mem)})
		}
	}
	return cpu, mem
}
