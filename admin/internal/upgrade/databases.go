package upgrade

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// --- stato dei database da ciò che Helm RENDE -----------------------------------------------------------
//
// Né l'abilitazione né la tabella si ricalcolano qui dai valori: sarebbe una seconda interpretazione di
// Helm (maiuscole di `true`, stringhe truthy, precedenza di -f e --set). Si legge invece il rendering che
// `helm template` ha già prodotto con gli stessi valori dell'upgrade: le variabili del Deployment di core
// (GITSTACK_CORE_DATABASES_ENABLED e GITSTACK_CORE_DATABASES_POSTGRES_IMAGES, templates/core/deployment.yaml)
// sono esattamente ciò che il controller riceve. Il chart ha poi values.schema.json, che rifiuta un
// databases.enabled non booleano prima del rendering. Un rendering che non si riesce a leggere, o con i
// database accesi e la tabella incompleta, ferma l'upgrade: mai ignorato.

const (
	envDatabasesEnabled = "GITSTACK_CORE_DATABASES_ENABLED"
	envDatabasesImages  = "GITSTACK_CORE_DATABASES_POSTGRES_IMAGES"
)

// databaseState è lo stato dei database gestiti in un rendering.
type databaseState struct {
	Enabled bool
	Images  []DatabaseImage
}

// databaseStateFromManifests legge lo stato dai manifest di helm template.
func databaseStateFromManifests(out []byte) (databaseState, error) {
	var st databaseState
	var imagesJSON string
	roleSeen := false
	dec := yaml.NewDecoder(bytes.NewReader(bytes.ReplaceAll(out, []byte("\r\n"), []byte("\n"))))
	for {
		var doc map[string]any
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return st, fmt.Errorf("manifest di helm template non leggibili: %w", err)
		}
		switch yamlStr(doc, "kind") {
		case "ClusterRole":
			if strings.HasSuffix(yamlStr(doc, "metadata", "name"), "-db-workload") {
				roleSeen = true
			}
		case "Deployment":
			pod, _ := yamlGet(doc, "spec", "template", "spec").(map[string]any)
			for _, key := range []string{"containers", "initContainers"} {
				cs, _ := pod[key].([]any)
				for _, c := range cs {
					envs, _ := yamlGet(c, "env").([]any)
					for _, e := range envs {
						switch yamlStr(e, "name") {
						case envDatabasesEnabled:
							if strings.EqualFold(yamlStr(e, "value"), "true") {
								st.Enabled = true
							}
						case envDatabasesImages:
							imagesJSON = yamlStr(e, "value")
						}
					}
				}
			}
		}
	}
	if roleSeen {
		st.Enabled = true
	}
	if !st.Enabled {
		return st, nil
	}
	if imagesJSON == "" {
		return st, errors.New("i database gestiti sono accesi ma il Deployment di core non riceve la tabella dei digest postgres (" + envDatabasesImages + ")")
	}
	var table map[string]struct {
		Repository string `json:"repository"`
		Tag        string `json:"tag"`
		Digest     string `json:"digest"`
	}
	if err := json.Unmarshal([]byte(imagesJSON), &table); err != nil {
		return st, fmt.Errorf("tabella dei digest postgres non leggibile: %w", err)
	}
	if len(table) == 0 {
		return st, errors.New("la tabella dei digest postgres (databases.postgres.images) è vuota ma i database gestiti sono accesi")
	}
	for major, im := range table {
		if im.Repository == "" || !digestRe.MatchString(im.Digest) {
			return st, fmt.Errorf("databases.postgres.images[%s]: servono repository e un digest sha256 (trovati %q e %q)", major, im.Repository, im.Digest)
		}
		st.Images = append(st.Images, DatabaseImage{Major: major, Repository: im.Repository, Tag: im.Tag, Digest: im.Digest})
	}
	sort.Slice(st.Images, func(i, j int) bool { return st.Images[i].Major < st.Images[j].Major })
	return st, nil
}

func yamlGet(m any, path ...string) any {
	for _, p := range path {
		mm, ok := m.(map[string]any)
		if !ok {
			return nil
		}
		m = mm[p]
	}
	return m
}

func yamlStr(m any, path ...string) string {
	s, _ := yamlGet(m, path...).(string)
	return s
}

// databaseStateBefore rende il chart ATTUALE con i soli valori conservati della release: com'era prima
// di questo aggiornamento. Illeggibile o assente = nessun riuso (si verifica tutto).
func (o *Options) databaseStateBefore(ctx context.Context, chartDir, valuesFile string) databaseState {
	args := []string{"template", o.Cfg.Release, chartDir, "-n", o.Cfg.Namespace, "-f", valuesFile, "--set", "global.image.tag=" + o.Cfg.ImageTag}
	out, err := o.helmRun(ctx, args...)
	if err != nil {
		return databaseState{}
	}
	st, err := databaseStateFromManifests(out)
	if err != nil {
		return databaseState{}
	}
	return st
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

// --- major in uso (DB6, GIT-557) ---------------------------------------------------------------------------
//
// Una release non può togliere dalla tabella (databases.postgres.images) una major di PostgreSQL che un
// database dell'istanza usa ancora: il controller non saprebbe più riavviarlo né aggiornarlo, e la major non
// cambia mai sul posto. Le major in uso si leggono dal database di core, la tabella dal rendering di Helm.

// majorsInUseQuery stampa le major di PostgreSQL dei database gestiti, una per riga. Senza la tabella
// (versione senza database gestiti) non stampa niente.
// Ogni errore di psql (connessione, autenticazione, SQL) esce con codice diverso da zero: mai «nessuna major».
const majorsInUseQuery = `exec_psql() { psql -X -q -At -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "$1"; }
exists=$(exec_psql "SELECT to_regclass('core.managed_dbs') IS NOT NULL") || exit $?
case "$exists" in
  t) exec_psql "SELECT DISTINCT pg_major FROM core.managed_dbs ORDER BY 1" || exit $? ;;
  f) exit 0 ;;
  *) echo "risposta inattesa di psql: $exists" >&2; exit 1 ;;
esac`

// parseMajors legge l'output di majorsInUseQuery; ignora ciò che non è un numero.
func parseMajors(out string) []int {
	var majors []int
	for _, l := range strings.Split(out, "\n") {
		n := 0
		if _, err := fmt.Sscanf(strings.TrimSpace(l), "%d", &n); err == nil && n > 0 {
			majors = append(majors, n)
		}
	}
	sort.Ints(majors)
	return majors
}

// databaseMajorsInUse legge le major usate dai database gestiti.
func (o *Options) databaseMajorsInUse(ctx context.Context) ([]int, error) {
	var b bytes.Buffer
	if err := o.Cluster.PGExec(ctx, nil, &b, "sh", "-c", majorsInUseQuery); err != nil {
		return nil, err
	}
	return parseMajors(b.String()), nil
}

// checkDatabaseMajors rifiuta la release che toglie una major ancora in uso (DB6). Vale anche con la
// funzione spenta nella destinazione (o già spenta): spegnere conserva i database, quindi non autorizza a
// togliere una major. Le major in uso si leggono sempre; una lettura impossibile ferma l'aggiornamento.
// Da spenta la tabella della release non è nel Deployment di core: si ricava da Helm rendendo il chart con
// gli stessi valori e databases.enabled=true (renderEnabled).
func (o *Options) checkDatabaseMajors(ctx context.Context, now databaseState, renderEnabled func(context.Context) (databaseState, error)) error {
	inUse, err := o.databaseMajorsInUse(ctx)
	if err != nil {
		return fmt.Errorf("non riesco a leggere le major di PostgreSQL usate dai database gestiti (%v): l'aggiornamento non può escludere che la release ne tolga una ancora in uso", err)
	}
	if len(inUse) == 0 {
		return nil
	}
	if !now.Enabled {
		if now, err = renderEnabled(ctx); err != nil {
			return fmt.Errorf("non riesco a ricavare la tabella delle major della release (%v): un database gestito usa ancora la major %d e l'aggiornamento non può escludere che la release la tolga", err, inUse[0])
		}
	}
	have := map[int]bool{}
	for _, im := range now.Images {
		var m int
		if _, err := fmt.Sscanf(im.Major, "%d", &m); err == nil {
			have[m] = true
		}
	}
	var missing []string
	for _, m := range inUse {
		if !have[m] {
			missing = append(missing, fmt.Sprintf("%d", m))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("la release non ha più la major PostgreSQL %s nella tabella databases.postgres.images, ma un database gestito la usa ancora (DB6): elimina o migra quei database prima, oppure scegli una release che la mantiene", strings.Join(missing, ", "))
	}
	return nil
}
