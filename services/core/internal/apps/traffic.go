package apps

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Richieste ed errori 5xx delle app (v3-a, GIT-403, H12). Traefik espone traefik_service_requests_total
// (contatore per service e codice HTTP) sull'entrypoint `metrics`, interno al cluster. Il controller lo
// legge a ogni giro delle metriche e scrive la differenza dall'ultima lettura. Il service di una
// IngressRoute CRD si chiama «<namespace>-<service>-<porta>@kubernetescrd»: ogni app ha un namespace
// suo (Namespace(id)), quindi le righe dell'app sono quelle che iniziano per «<namespace>-» (il formato
// reale lo prova il test k3d, non solo il parsing).

const (
	traefikRequestsMetric = "traefik_service_requests_total"
	traefikCRDSuffix      = "@kubernetescrd"
	traefikFetchTimeout   = 5 * time.Second
	traefikMaxBody        = 32 << 20
)

// TraefikMetricsReader dà il testo Prometheus delle metriche di Traefik. Un errore vuol dire «sorgente
// non risponde».
type TraefikMetricsReader func(ctx context.Context) ([]byte, error)

// trafficCounters sono i contatori cumulativi di un'app: tutte le richieste e quelle con codice 5xx.
type trafficCounters struct {
	Requests  float64
	Errors5xx float64
}

func (c *Controller) readTraefikMetrics(ctx context.Context) ([]byte, error) {
	if c.cfg.TraefikMetrics != nil {
		return c.cfg.TraefikMetrics(ctx)
	}
	if c.cfg.TraefikMetricsURL == "" {
		return nil, fmt.Errorf("metriche di Traefik non configurate")
	}
	ctx, cancel := context.WithTimeout(ctx, traefikFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.TraefikMetricsURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("metriche di Traefik: stato %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, traefikMaxBody))
}

// parseTraefikTraffic somma i contatori dei service del namespace ns nel testo Prometheus di Traefik.
// found è falso se nessuna riga appartiene al namespace (Traefik non espone una serie finché non vede
// una richiesta).
func parseTraefikTraffic(text []byte, ns string) (t trafficCounters, found bool, err error) {
	prefix := ns + "-"
	sc := bufio.NewScanner(bytes.NewReader(text))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, traefikRequestsMetric+"{") {
			continue
		}
		labels, rest, perr := parseMetricLabels(line[len(traefikRequestsMetric):])
		if perr != nil {
			return t, false, fmt.Errorf("riga %q: %w", line, perr)
		}
		svc := labels["service"]
		if !strings.HasPrefix(svc, prefix) || !strings.HasSuffix(svc, traefikCRDSuffix) {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return t, false, fmt.Errorf("riga %q: valore mancante", line)
		}
		v, perr := strconv.ParseFloat(fields[0], 64)
		if perr != nil || v < 0 {
			return t, false, fmt.Errorf("riga %q: valore non valido", line)
		}
		found = true
		t.Requests += v
		if code := labels["code"]; len(code) == 3 && code[0] == '5' {
			t.Errors5xx += v
		}
	}
	if err := sc.Err(); err != nil {
		return t, false, err
	}
	return t, found, nil
}

// parseMetricLabels legge «{a="x",b="y"} resto» e dà le etichette e quel che segue la graffa.
func parseMetricLabels(s string) (map[string]string, string, error) {
	if !strings.HasPrefix(s, "{") {
		return nil, "", fmt.Errorf("etichette mancanti")
	}
	labels := map[string]string{}
	i := 1
	for {
		for i < len(s) && (s[i] == ',' || s[i] == ' ') {
			i++
		}
		if i >= len(s) {
			return nil, "", fmt.Errorf("etichette non chiuse")
		}
		if s[i] == '}' {
			return labels, s[i+1:], nil
		}
		eq := strings.IndexByte(s[i:], '=')
		if eq < 0 || i+eq+1 >= len(s) || s[i+eq+1] != '"' {
			return nil, "", fmt.Errorf("etichetta non valida")
		}
		name := strings.TrimSpace(s[i : i+eq])
		i += eq + 2
		var val strings.Builder
		for {
			if i >= len(s) {
				return nil, "", fmt.Errorf("valore non chiuso")
			}
			ch := s[i]
			if ch == '\\' && i+1 < len(s) {
				switch s[i+1] {
				case 'n':
					val.WriteByte('\n')
				default:
					val.WriteByte(s[i+1])
				}
				i += 2
				continue
			}
			if ch == '"' {
				i++
				break
			}
			val.WriteByte(ch)
			i++
		}
		labels[name] = val.String()
	}
}

// trafficDelta dà richieste ed errori nuovi rispetto alla lettura precedente. Senza lettura precedente
// (core appena partito) la base è la lettura stessa; un contatore che scende (Traefik riavviato) riparte
// da zero, quindi vale il suo valore attuale. Gli errori non superano mai le richieste.
func trafficDelta(prev trafficCounters, hadPrev bool, cur trafficCounters) (req, errs int64) {
	if !hadPrev {
		return 0, 0
	}
	dr, de := cur.Requests-prev.Requests, cur.Errors5xx-prev.Errors5xx
	if dr < 0 || de < 0 {
		dr, de = cur.Requests, cur.Errors5xx
	}
	req, errs = int64(dr), int64(de)
	if errs > req {
		errs = req
	}
	return req, errs
}

// collectTraffic legge i contatori di Traefik una volta per giro (text) e scrive il campione di un'app.
// Una sorgente che non risponde non scrive niente: l'API dirà «non disponibile».
func (c *Controller) collectTraffic(ctx context.Context, appID uuid.UUID, text []byte, now time.Time) {
	cur, found, err := parseTraefikTraffic(text, Namespace(appID))
	if err != nil {
		c.cfg.Log.Warn("apps: metriche di Traefik non leggibili", "app", appID, "err", err)
		return
	}
	// Senza serie Traefik non ha visto richieste: il contatore vale zero, e la prima richiesta sarà conteggiata.
	prev, hadPrev := c.traffic[appID]
	if !found && !hadPrev {
		prev, hadPrev = trafficCounters{}, true
	}
	c.traffic[appID] = cur
	req, errs := trafficDelta(prev, hadPrev, cur)
	if err := c.st.AppendAppTrafficSample(ctx, appID, store.AppTrafficSample{At: now, Requests: req, Errors5xx: errs}); err != nil && ctx.Err() == nil {
		c.cfg.Log.Warn("apps: campione di richieste non scritto", "app", appID, "err", err)
	}
}
