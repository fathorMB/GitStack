package backup

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Immagini del registry nel backup (GIT-325, RG2). Le immagini OCI stanno nel volume git-data,
// in <volume>/registry (disco; con S3 non sono nel volume e il backup non le copia). Di default
// restano fuori dal backup; entrano solo quelle dei repo con includeInBackup e, quando la v3
// (H4) le fornirà con Options.InUseRepoIDs, quelle in uso dalle app.
//
// Il registry archivia i blob per contenuto, condivisi fra i repo: per includere solo alcune
// immagini si copiano le cartelle dei repo scelti (repositories/<id>) e, fra i blob, quelli
// che i loro layer e manifest referenziano. In restore il tar si estrae com'è: il registry
// torna con le sole immagini incluse.

// registryDirName è la cartella del registry dentro il volume git-data (config.EnvRegistryDir
// di services/git: <dati>/registry).
const registryDirName = "registry"

const registryV2 = registryDirName + "/docker/registry/v2"

var repoIDRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// includedRepoCmd legge i repo con includeInBackup. Se la tabella non esiste (database di
// una versione precedente, per esempio il backup prima di un aggiornamento) l'elenco è vuoto.
const includedRepoCmd = `exec_psql() { psql -q -At -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "$1"; }
if [ "$(exec_psql "SELECT to_regclass('core.repo_package_settings') IS NOT NULL")" = t ]; then
  exec_psql "SELECT repo_id FROM core.repo_package_settings WHERE include_in_backup ORDER BY repo_id"
fi`

// registryRepos ritorna gli id dei repo le cui immagini entrano nel backup.
func (o *Options) registryRepos(ctx context.Context) ([]string, error) {
	var out bytes.Buffer
	if err := o.Cluster.PGExec(ctx, nil, &out, "sh", "-c", includedRepoCmd); err != nil {
		return nil, fmt.Errorf("elenco dei repo con le immagini nel backup: %w", err)
	}
	set := map[string]bool{}
	for _, line := range strings.Split(out.String(), "\n") {
		if id := strings.ToLower(strings.TrimSpace(line)); repoIDRe.MatchString(id) {
			set[id] = true
		}
	}
	if o.InUseRepoIDs != nil {
		ids, err := o.InUseRepoIDs(ctx)
		if err != nil {
			return nil, fmt.Errorf("immagini in uso dalle app: %w", err)
		}
		for _, id := range ids {
			if id = strings.ToLower(strings.TrimSpace(id)); repoIDRe.MatchString(id) {
				set[id] = true
			}
		}
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// registryDigests raccoglie i digest (hex) dei blob referenziati dai repo scelti: layer
// (config compreso) e manifest.
func registryDigests(volume string, repoIDs []string) (map[string]bool, error) {
	digests := map[string]bool{}
	repos := filepath.Join(volume, filepath.FromSlash(registryV2), "repositories")
	for _, id := range repoIDs {
		root := filepath.Join(repos, id)
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				return nil
			}
			slash := filepath.ToSlash(p)
			if strings.HasSuffix(slash, "/_layers/sha256") || strings.HasSuffix(slash, "/_manifests/revisions/sha256") {
				entries, err := os.ReadDir(p)
				if err != nil {
					return err
				}
				for _, e := range entries {
					if e.IsDir() {
						digests[e.Name()] = true
					}
				}
				return filepath.SkipDir
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	return digests, nil
}

// registrySkip ritorna il filtro di tarDirSkip: dentro il registry si tengono le cartelle
// di struttura, i repo scelti e i loro blob; il resto (e gli upload in corso) no.
func registrySkip(volume string, repoIDs []string) (func(rel string, isDir bool) bool, error) {
	digests, err := registryDigests(volume, repoIDs)
	if err != nil {
		return nil, err
	}
	chosen := map[string]bool{}
	for _, id := range repoIDs {
		chosen[id] = true
	}
	return func(rel string, isDir bool) bool {
		if rel != registryDirName && !strings.HasPrefix(rel, registryDirName+"/") {
			return false
		}
		if rel == registryDirName {
			return false
		}
		if strings.Contains(rel, "/_uploads") {
			return true
		}
		if !strings.HasPrefix(rel+"/", registryV2+"/") {
			// Catena di cartelle verso registry/docker/registry/v2: si tiene la sola struttura.
			return !isDir
		}
		tail := strings.TrimPrefix(rel, registryV2)
		parts := strings.Split(strings.Trim(tail, "/"), "/")
		switch {
		case tail == "":
			return false
		case parts[0] == "repositories":
			if len(parts) == 1 {
				return false
			}
			return !chosen[parts[1]]
		case parts[0] == "blobs":
			// blobs/sha256/<xx>/<hex>[/data]
			if len(parts) <= 3 {
				return !isDir
			}
			return !digests[parts[3]]
		}
		return true
	}, nil
}
