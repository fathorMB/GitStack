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

func readDatabaseImages(chartDir string) ([]DatabaseImage, error) {
	b, err := os.ReadFile(filepath.Join(chartDir, "values.yaml"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return databaseImagesFromValues(b)
}

// databasesEnabledIn dice se databases.enabled è acceso in un albero di valori.
func databasesEnabledIn(vals map[string]any) (bool, bool) {
	d, _ := vals["databases"].(map[string]any)
	if d == nil {
		return false, false
	}
	switch e := d["enabled"].(type) {
	case bool:
		return e, true
	case string:
		return e == "true", true
	}
	return false, false
}

// databasesEnabled dice se i database gestiti sono accesi con i valori della release (valuesFile, JSON di
// helm get values), i file -f e i --set di questo aggiornamento (l'ultimo vince, come in helm).
// before è il valore dei soli valori della release, cioè com'era prima di questo aggiornamento.
func (o *Options) databasesEnabled(valuesFile string) (now, before bool) {
	if b, err := os.ReadFile(valuesFile); err == nil {
		var vals map[string]any
		if json.Unmarshal(b, &vals) == nil {
			before, _ = databasesEnabledIn(vals)
		}
	}
	now = before
	for _, f := range o.ValueFiles {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var vals map[string]any
		if yaml.Unmarshal(b, &vals) == nil {
			if e, ok := databasesEnabledIn(vals); ok {
				now = e
			}
		}
	}
	for _, s := range o.Sets {
		if v, ok := strings.CutPrefix(s, "databases.enabled="); ok {
			now = v == "true"
		}
	}
	return now, before
}

// verifyDatabaseDigests verifica con cosign i digest postgres nuovi della tabella del chart di
// destinazione. Con i database già accesi si verificano solo le righe che la copia attuale del chart non
// aveva (digest nuovi); alla prima accensione tutte. Ritorna i riferimenti verificati.
func (o *Options) verifyDatabaseDigests(ctx context.Context, newChart, chartDir, valuesFile string) ([]string, error) {
	now, before := o.databasesEnabled(valuesFile)
	if !now {
		return nil, nil
	}
	table, err := readDatabaseImages(newChart)
	if err != nil {
		return nil, fmt.Errorf("tabella dei digest postgres del chart: %w", err)
	}
	if len(table) == 0 {
		return nil, errors.New("il chart di destinazione non ha la tabella dei digest postgres (databases.postgres.images) ma i database gestiti sono accesi")
	}
	known := map[string]bool{}
	if before {
		old, err := readDatabaseImages(chartDir)
		if err != nil {
			return nil, fmt.Errorf("tabella dei digest postgres attuale: %w", err)
		}
		for _, im := range old {
			known[im.Digest] = true
		}
	}
	var done []string
	for _, im := range table {
		if known[im.Digest] {
			continue
		}
		ref := im.Ref()
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
func (o *Options) ensureDatabaseSecrets(ctx context.Context, res *Result, valuesFile string) error {
	if now, _ := o.databasesEnabled(valuesFile); !now {
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
