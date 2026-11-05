package upgrade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/fathorMB/GitStack/admin/internal/dbpki"
)

// Database gestiti (ADR 0003, GIT-565).
//
// Il chart porta la tabella major → digest delle immagini PostgreSQL (databases.postgres.images, DB5).
// Le immagini non compaiono nei manifest: le usa il controller di core per creare gli StatefulSet, quindi
// il giro delle immagini di helm template non le vede. `gitstack upgrade` le verifica a parte, con lo
// stesso cosign (H10): prima di applicare il chart, per ogni digest NUOVO della tabella. Firma mancante o
// sbagliata ferma l'aggiornamento (Refused), salvo l'eccezione N9 dell'installazione.
//
// Intermedia dei database e chiave di dump: Ensure (admin/internal/dbpki) le crea se mancano, prima di
// helm upgrade, perché così core parte già con la chiave di dump (la monta come volume opzionale).

// DatabaseImage è una riga della tabella major → digest.
type DatabaseImage struct {
	Major      string
	Repository string
	Tag        string
	Digest     string
}

// Ref è il riferimento per digest che si verifica e che il controller userà.
func (d DatabaseImage) Ref() string { return d.Repository + "@" + d.Digest }

var digestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// databaseImagesFromValues legge databases.postgres.images da un values.yaml. Ordinata per major.
func databaseImagesFromValues(data []byte) ([]DatabaseImage, error) {
	var v struct {
		Databases struct {
			Postgres struct {
				Images map[string]struct {
					Repository string `yaml:"repository"`
					Tag        string `yaml:"tag"`
					Digest     string `yaml:"digest"`
				} `yaml:"images"`
			} `yaml:"postgres"`
		} `yaml:"databases"`
	}
	if err := yaml.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	var out []DatabaseImage
	for major, im := range v.Databases.Postgres.Images {
		if im.Digest == "" {
			continue
		}
		if !digestRe.MatchString(im.Digest) {
			return nil, fmt.Errorf("databases.postgres.images[%s].digest %q non è un digest sha256", major, im.Digest)
		}
		if im.Repository == "" {
			return nil, fmt.Errorf("databases.postgres.images[%s].repository mancante", major)
		}
		out = append(out, DatabaseImage{Major: major, Repository: im.Repository, Tag: im.Tag, Digest: im.Digest})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Major < out[j].Major })
	return out, nil
}

// --- valori effettivi, con la precedenza e la semantica di Helm ----------------------------------------
//
// Core riceve la tabella dalla release (deployment.yaml: .Values.databases.postgres.images), cioè dal
// risultato di: default del chart < valori conservati della release (helm get values) < -f in ordine <
// --set. Un override può quindi sostituire repository o digest di una major: la firma si verifica sul
// riferimento repository@digest EFFETTIVO, non sui soli default del chart.

// normalizeYAML porta le mappe di yaml.v3 (chiavi non stringa) a map[string]any, ricorsivamente.
func normalizeYAML(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			x[k] = normalizeYAML(e)
		}
		return x
	case map[any]any:
		m := make(map[string]any, len(x))
		for k, e := range x {
			m[fmt.Sprint(k)] = normalizeYAML(e)
		}
		return m
	case []any:
		for i, e := range x {
			x[i] = normalizeYAML(e)
		}
		return x
	}
	return v
}

// mergeValues sovrappone src a dst come fa Helm: le mappe si fondono, il resto sostituisce, null toglie.
func mergeValues(dst, src map[string]any) map[string]any {
	if dst == nil {
		dst = map[string]any{}
	}
	for k, v := range src {
		if v == nil {
			delete(dst, k)
			continue
		}
		if sm, ok := v.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				dst[k] = mergeValues(dm, sm)
				continue
			}
			dst[k] = mergeValues(nil, sm)
			continue
		}
		dst[k] = v
	}
	return dst
}

// splitUnescaped divide s su sep, tranne dove è preceduto da una barra rovesciata (come --set di Helm).
func splitUnescaped(s string, sep byte) []string {
	var out []string
	var cur strings.Builder
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\\' && i+1 < len(s) && s[i+1] == sep:
			cur.WriteByte(sep)
			i++
		case s[i] == sep:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(s[i])
		}
	}
	return append(out, cur.String())
}

// setValue applica un `--set chiave.annidata=valore` (valori scalari: true/false/null/intero/stringa).
// Le liste (`a[0]=x`) non servono per questa tabella e si ignorano.
func setValue(vals map[string]any, kv string) {
	key, raw, ok := strings.Cut(kv, "=")
	if !ok || strings.Contains(key, "[") {
		return
	}
	var val any = raw
	switch raw {
	case "true":
		val = true
	case "false":
		val = false
	case "null":
		val = nil
	default:
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && (raw == "0" || !strings.HasPrefix(raw, "0")) {
			val = n
		}
	}
	parts := splitUnescaped(key, '.')
	cur := vals
	for _, p := range parts[:len(parts)-1] {
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
	last := parts[len(parts)-1]
	if val == nil {
		delete(cur, last)
		return
	}
	cur[last] = val
}

// effectiveValues risolve i valori di una release: default del chart in chartDir, poi i valori
// conservati (JSON di helm get values), e solo con withInputs i -f e i --set di questo aggiornamento.
func (o *Options) effectiveValues(chartDir, valuesFile string, withInputs bool) (map[string]any, error) {
	vals := map[string]any{}
	if b, err := os.ReadFile(filepath.Join(chartDir, "values.yaml")); err == nil {
		var m map[string]any
		if err := yaml.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("values.yaml del chart %s: %w", chartDir, err)
		}
		vals = mergeValues(vals, normalizeYAML(m).(map[string]any))
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if b, err := os.ReadFile(valuesFile); err == nil {
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("valori della release: %w", err)
		}
		vals = mergeValues(vals, m)
	}
	if !withInputs {
		return vals, nil
	}
	for _, f := range o.ValueFiles {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("file di valori %s: %w", f, err)
		}
		var m map[string]any
		if err := yaml.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("file di valori %s: %w", f, err)
		}
		if m != nil {
			vals = mergeValues(vals, normalizeYAML(m).(map[string]any))
		}
	}
	for _, s := range o.Sets {
		for _, kv := range splitUnescaped(s, ',') {
			setValue(vals, kv)
		}
	}
	return vals, nil
}

// databaseState è lo stato dei database gestiti in un insieme di valori effettivi.
type databaseState struct {
	Enabled bool
	Images  []DatabaseImage
}

func databaseStateOf(vals map[string]any) (databaseState, error) {
	var st databaseState
	d, _ := vals["databases"].(map[string]any)
	if d == nil {
		return st, nil
	}
	switch e := d["enabled"].(type) {
	case bool:
		st.Enabled = e
	case string:
		st.Enabled = e == "true"
	}
	b, err := yaml.Marshal(map[string]any{"databases": d})
	if err != nil {
		return st, err
	}
	st.Images, err = databaseImagesFromValues(b)
	return st, err
}

// databaseStates: stato prima (chart attuale + valori conservati) e dopo (chart nuovo + valori conservati
// + -f + --set) di questo aggiornamento.
func (o *Options) databaseStates(newChart, chartDir, valuesFile string) (now, before databaseState, err error) {
	nv, err := o.effectiveValues(newChart, valuesFile, true)
	if err != nil {
		return now, before, err
	}
	if now, err = databaseStateOf(nv); err != nil {
		return now, before, fmt.Errorf("tabella dei digest postgres effettiva: %w", err)
	}
	ov, err := o.effectiveValues(chartDir, valuesFile, false)
	if err != nil {
		return now, before, err
	}
	// La tabella di prima illeggibile o incompleta non blocca: vuol dire solo che non si riusa niente.
	if before, err = databaseStateOf(ov); err != nil {
		before = databaseState{}
	}
	return now, before, nil
}

// verifyDatabaseDigests verifica con cosign i riferimenti repository@digest postgres EFFETTIVI nuovi.
// Con i database già accesi si salta ciò che era già nella tabella attuale (stesso repository@digest:
// riuso); alla prima accensione si verifica tutta. Ritorna i riferimenti verificati.
func (o *Options) verifyDatabaseDigests(ctx context.Context, now, before databaseState) ([]string, error) {
	if !now.Enabled {
		return nil, nil
	}
	if len(now.Images) == 0 {
		return nil, errors.New("la tabella dei digest postgres (databases.postgres.images) è vuota ma i database gestiti sono accesi")
	}
	known := map[string]bool{}
	if before.Enabled {
		for _, im := range before.Images {
			known[im.Ref()] = true
		}
	}
	var done []string
	for _, im := range now.Images {
		ref := im.Ref()
		if known[ref] {
			continue
		}
		if o.v5().AllowUnsigned {
			o.logf("    ok %s (postgres %s: firma NON verificata: eccezione N9)", ref, im.Major)
			continue
		}
		if err := o.v5().VerifyImage(ctx, ref); err != nil {
			return nil, fmt.Errorf("immagine postgres %s (major %s) della tabella dei database: %v. Per un mirror interno non firmato serve --allow-unsigned-mirrors (N9)", ref, im.Major, err)
		}
		o.logf("    ok %s (postgres %s: firma cosign verificata)", ref, im.Major)
		done = append(done, ref)
	}
	return done, nil
}

// ensureDatabaseSecrets crea la CA intermedia dei database e la chiave di dump se mancano (mai se
// esistono). Senza la radice sull'host (certificato del cliente) l'intermedia non si può creare:
// avvisa nel riepilogo e prosegue, la chiave di dump c'è comunque.
func (o *Options) ensureDatabaseSecrets(ctx context.Context, res *Result, now databaseState) error {
	if !now.Enabled {
		return nil
	}
	tlsDir := o.TLSDir
	if tlsDir == "" {
		tlsDir = filepath.Join(filepath.Dir(o.ConfigFile), "tls")
	}
	r, err := dbpki.Ensure(ctx, dbpki.Options{Cluster: o.Cluster, Release: o.Cfg.Release, TLSDir: tlsDir})
	if r.CreatedDumpKey {
		o.logf("==> Database gestiti: creata la chiave di dump (%s)", dbpki.DumpKeySecretName(o.Cfg.Release))
	}
	if r.CreatedCA {
		o.logf("==> Database gestiti: creata la CA intermedia (%s)", dbpki.CASecretName(o.Cfg.Release))
	}
	switch {
	case errors.Is(err, dbpki.ErrNoRootCA):
		res.add("Database gestiti: CA intermedia NON creata, la radice dell'istanza non è sull'host (%v). I database con TLS non partiranno finché non c'è.", err)
		return nil
	case err != nil:
		return fmt.Errorf("segreti dei database gestiti: %w", err)
	}
	return nil
}
