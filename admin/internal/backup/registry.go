package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Immagini del registry nel backup (GIT-325, RG2, H4). Le immagini OCI stanno nel volume git-data,
// in <volume>/registry (disco; con S3 non sono nel volume e il backup non le copia). Di default
// restano fuori dal backup. Entrano:
//   - tutte le immagini dei repo con includeInBackup (cartella del repo e blob che referenzia);
//   - le immagini in uso dalle app v3: la versione attiva e la precedente, solo quei tag (o
//     digest) con i manifest e i blob che servono; il resto del repo resta fuori (H4).
//
// Il registry archivia i blob per contenuto, condivisi fra i repo: il filtro tiene solo i percorsi
// scelti, più le cartelle che li contengono. In restore il tar si estrae com'è: il registry torna
// con le sole immagini incluse.

// registryDirName è la cartella del registry dentro il volume git-data (config.EnvRegistryDir
// di services/git: <dati>/registry).
const registryDirName = "registry"

const registryV2 = registryDirName + "/docker/registry/v2"

var (
	repoIDRe   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	hexDigest  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	imageName  = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*$`)
	imageTagRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
)

// includedRepoCmd legge i repo con includeInBackup. Se la tabella non esiste (database di
// una versione precedente, per esempio il backup prima di un aggiornamento) l'elenco è vuoto.
const includedRepoCmd = `exec_psql() { psql -q -At -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "$1"; }
if [ "$(exec_psql "SELECT to_regclass('core.repo_package_settings') IS NOT NULL")" = t ]; then
  exec_psql "SELECT repo_id FROM core.repo_package_settings WHERE include_in_backup ORDER BY repo_id"
fi`

// inUseCmd legge le immagini del registry in uso dalle app: la versione attiva e la precedente
// (H4). Una riga per immagine: repo_id|owner|repo|riferimento|digest (digest vuoto se per tag).
const inUseCmd = `exec_psql() { psql -q -At -F '|' -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "$1"; }
if [ "$(exec_psql "SELECT to_regclass('core.app_versions') IS NOT NULL")" = t ]; then
  exec_psql "SELECT a.repo_id, r.owner_name, r.name, v.image_reference, COALESCE(v.image_digest, '') FROM core.apps a JOIN core.repositories r ON r.resource_id = a.repo_id JOIN core.app_versions v ON v.app_id = a.id AND v.number IN (a.active_version, a.active_version - 1) WHERE v.image_source = 'registry' ORDER BY a.repo_id, v.image_reference"
fi`

// InUseImage è un'immagine del registry in uso da un'app (versione attiva o precedente).
type InUseImage struct {
	RepoID    string // resource_id del repo
	Owner     string // owner del repo, come nel riferimento
	Repo      string // nome del repo
	Reference string // riferimento salvato: host/owner/repo[/nome]:tag oppure ...@sha256:...
	Digest    string // digest salvato (vuoto se la versione è per tag)
}

// registryRepos ritorna gli id dei repo con includeInBackup: di questi entra tutto il repo.
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
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// inUseImages ritorna le immagini in uso dalle app: dal hook InUseImages se c'è (test), altrimenti
// dal database.
func (o *Options) inUseImages(ctx context.Context) ([]InUseImage, error) {
	if o.InUseImages != nil {
		return o.InUseImages(ctx)
	}
	var out bytes.Buffer
	if err := o.Cluster.PGExec(ctx, nil, &out, "sh", "-c", inUseCmd); err != nil {
		return nil, fmt.Errorf("immagini in uso dalle app: %w", err)
	}
	var imgs []InUseImage
	for _, line := range strings.Split(out.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		f := strings.Split(line, "|")
		if len(f) != 5 {
			return nil, fmt.Errorf("riga delle immagini in uso non valida (%d campi)", len(f))
		}
		imgs = append(imgs, InUseImage{RepoID: strings.ToLower(f[0]), Owner: f[1], Repo: f[2], Reference: f[3], Digest: f[4]})
	}
	return imgs, nil
}

// inUseRefs dà i riferimenti delle immagini in uso, per il manifest.
func inUseRefs(imgs []InUseImage) []string {
	var out []string
	for _, img := range imgs {
		out = append(out, img.Reference)
	}
	return out
}

// imageSel è un'immagine in uso letta nel registry: repo, nome nel repo, tag o digest.
type imageSel struct {
	repoID, name, tag, digest string
}

// parseInUse legge il riferimento salvato dall'app: <host>/<owner>/<repo>[/<nome>][:<tag>|@sha256:<hex>].
func parseInUse(img InUseImage) (imageSel, error) {
	bad := func(why string) (imageSel, error) {
		return imageSel{}, fmt.Errorf("immagine in uso %q del repo %s: %s", img.Reference, img.RepoID, why)
	}
	if !repoIDRe.MatchString(img.RepoID) {
		return bad("id del repo non valido")
	}
	ref := img.Reference
	marker := "/" + strings.ToLower(img.Owner) + "/" + strings.ToLower(img.Repo)
	i := strings.Index(strings.ToLower(ref), marker)
	if i < 0 {
		return bad("il riferimento non è del repo")
	}
	rest := ref[i+len(marker):]
	if rest != "" && rest[0] != '/' && rest[0] != ':' && rest[0] != '@' {
		return bad("il riferimento non è del repo")
	}
	var sel imageSel
	sel.repoID = strings.ToLower(img.RepoID)
	if j := strings.Index(rest, "@"); j >= 0 {
		sel.digest = rest[j+1:]
		rest = rest[:j]
	}
	if j := strings.LastIndex(rest, ":"); j >= 0 && j > strings.LastIndex(rest, "/") {
		sel.tag = rest[j+1:]
		rest = rest[:j]
	}
	sel.name = strings.TrimPrefix(rest, "/")
	if sel.name != "" && !imageName.MatchString(sel.name) {
		return bad("nome dell'immagine non valido")
	}
	if sel.digest == "" {
		sel.digest = img.Digest
	}
	if sel.digest != "" {
		h, ok := strings.CutPrefix(sel.digest, "sha256:")
		if !ok || !hexDigest.MatchString(h) {
			return bad("digest non valido")
		}
		sel.digest = h
	}
	if sel.tag != "" && !imageTagRe.MatchString(sel.tag) {
		return bad("tag non valido")
	}
	if sel.tag == "" && sel.digest == "" {
		return bad("manca il tag o il digest")
	}
	return sel, nil
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

// manifestDoc copre i campi di un manifest OCI/Docker che portano riferimenti ad altri blob.
type manifestDoc struct {
	Manifests []struct {
		Digest string `json:"digest"`
	} `json:"manifests"`
	Config *struct {
		Digest string `json:"digest"`
	} `json:"config"`
	Layers []struct {
		Digest string `json:"digest"`
	} `json:"layers"`
}

// inUseKeep aggiunge a keep i percorsi (relativi al volume git-data, con /) delle immagini in
// uso: il tag (solo il current), la revisione del manifest, i link dei layer e i blob. Per un
// manifest index segue i manifest figli, fino a due livelli.
func inUseKeep(volume string, imgs []InUseImage, keep map[string]bool) error {
	for _, img := range imgs {
		sel, err := parseInUse(img)
		if err != nil {
			return err
		}
		base := registryV2 + "/repositories/" + sel.repoID
		if sel.name != "" {
			base += "/" + sel.name
		}
		hex := sel.digest
		if sel.tag != "" {
			link, err := os.ReadFile(filepath.Join(volume, filepath.FromSlash(base+"/_manifests/tags/"+sel.tag+"/current/link")))
			if err != nil {
				return fmt.Errorf("immagine in uso %q: tag %s non trovato nel registry: %w", img.Reference, sel.tag, err)
			}
			h, ok := strings.CutPrefix(strings.TrimSpace(string(link)), "sha256:")
			if !ok || !hexDigest.MatchString(h) {
				return fmt.Errorf("immagine in uso %q: link del tag non valido", img.Reference)
			}
			if hex != "" && hex != h {
				return fmt.Errorf("immagine in uso %q: il tag punta a un altro digest di quello salvato", img.Reference)
			}
			hex = h
			keep[base+"/_manifests/tags/"+sel.tag+"/current"] = true
		}
		if err := keepManifest(volume, base, hex, keep, 0); err != nil {
			return fmt.Errorf("immagine in uso %q: %w", img.Reference, err)
		}
	}
	return nil
}

// keepManifest segue un manifest: la sua revisione, il blob, config e layer (con i link nel repo).
func keepManifest(volume, base, hex string, keep map[string]bool, depth int) error {
	if depth > 2 {
		return errors.New("manifest annidati oltre due livelli")
	}
	if !hexDigest.MatchString(hex) {
		return errors.New("digest del manifest non valido")
	}
	keep[base+"/_manifests/revisions/sha256/"+hex] = true
	blob := registryV2 + "/blobs/sha256/" + hex[:2] + "/" + hex
	keep[blob] = true
	b, err := os.ReadFile(filepath.Join(volume, filepath.FromSlash(blob), "data"))
	if err != nil {
		return fmt.Errorf("blob del manifest %s mancante: %w", hex, err)
	}
	var doc manifestDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("manifest %s non leggibile: %w", hex, err)
	}
	add := func(d string) error {
		h, ok := strings.CutPrefix(d, "sha256:")
		if !ok || !hexDigest.MatchString(h) {
			return fmt.Errorf("digest %q non supportato", d)
		}
		keep[base+"/_layers/sha256/"+h] = true
		keep[registryV2+"/blobs/sha256/"+h[:2]+"/"+h] = true
		return nil
	}
	if doc.Config != nil {
		if err := add(doc.Config.Digest); err != nil {
			return err
		}
	}
	for _, l := range doc.Layers {
		if err := add(l.Digest); err != nil {
			return err
		}
	}
	for _, m := range doc.Manifests {
		h, ok := strings.CutPrefix(m.Digest, "sha256:")
		if !ok || !hexDigest.MatchString(h) {
			return fmt.Errorf("digest %q non supportato", m.Digest)
		}
		if err := keepManifest(volume, base, h, keep, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// registrySkip ritorna il filtro di tarDirSkip. Dentro il registry si tengono le cartelle di
// struttura, i repo con includeInBackup (con i loro blob) e le immagini in uso (solo i loro
// percorsi); il resto, e gli upload in corso, no.
func registrySkip(volume string, repoIDs []string, inUse []InUseImage) (func(rel string, isDir bool) bool, error) {
	digests, err := registryDigests(volume, repoIDs)
	if err != nil {
		return nil, err
	}
	keep := map[string]bool{}
	for _, id := range repoIDs {
		keep[registryV2+"/repositories/"+id] = true
	}
	for d := range digests {
		if hexDigest.MatchString(d) {
			keep[registryV2+"/blobs/sha256/"+d[:2]+"/"+d] = true
		}
	}
	if err := inUseKeep(volume, inUse, keep); err != nil {
		return nil, err
	}
	// Le cartelle che portano a un percorso tenuto sono strutturali: si tengono anche loro.
	anc := map[string]bool{}
	for k := range keep {
		for p := parentOf(k); p != ""; p = parentOf(p) {
			anc[p] = true
		}
	}
	return func(rel string, isDir bool) bool {
		if rel != registryDirName && !strings.HasPrefix(rel, registryDirName+"/") {
			return false
		}
		if rel == registryDirName || rel == registryV2 {
			return false
		}
		if strings.Contains(rel, "/_uploads") {
			return true
		}
		if !strings.HasPrefix(rel, registryV2+"/") {
			// Catena di cartelle sopra registry/docker/registry/v2: si tiene la sola struttura.
			return !isDir
		}
		if tenuto(rel, keep) {
			return false
		}
		return !(isDir && anc[rel])
	}, nil
}

// tenuto dice se rel è un percorso tenuto o sta dentro uno di loro.
func tenuto(rel string, keep map[string]bool) bool {
	for p := rel; p != ""; p = parentOf(p) {
		if keep[p] {
			return true
		}
	}
	return false
}

func parentOf(p string) string {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return ""
	}
	return p[:i]
}
