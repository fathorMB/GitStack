package backup

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// restorePrelude prepara Postgres: il ruolo di identity deve esistere (il
// dump ne assegna a lui la proprietà dello schema; la password la riallinea
// l'initContainer di identity dal Secret ripristinato) e gli schemi vecchi
// vanno tolti. Gira nella stessa transazione del dump.
const restorePrelude = `DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'identity_app') THEN
    CREATE ROLE identity_app NOLOGIN;
  END IF;
END $$;
DROP SCHEMA IF EXISTS identity CASCADE;
DROP SCHEMA IF EXISTS core CASCADE;
`

// Restore ripristina l'archivio su un'installazione pulita della stessa
// versione.
//
// Gli ingressi e i servizi che usano database e volumi restano fermi
// finché il ripristino non è completo; se un passo fallisce restano fermi
// (un database a metà non deve ricevere traffico) e l'errore dice come
// riaccenderli. Il Secret di Postgres non si ripristina: la password è
// quella del Postgres della nuova installazione.
func Restore(ctx context.Context, o *Options, archive string) (err error) {
	if o.Cfg.ImageTag == "" {
		return errors.New("image_tag assente dal config: la versione del server non è nota")
	}
	tr, closeFn, err := openArchive(archive, o.Key)
	if err != nil {
		return err
	}
	defer closeFn()

	hdr, err := tr.Next()
	if err != nil || hdr.Name != ManifestName {
		return refused("archivio non valido: manca %s come prima voce", ManifestName)
	}
	var m Manifest
	if err := json.NewDecoder(io.LimitReader(tr, 1<<20)).Decode(&m); err != nil {
		return refused("manifest non leggibile: %v", err)
	}
	if m.FormatVersion != FormatVersion {
		return refused("formato dell'archivio %d non supportato da questo gitstack (atteso %d)", m.FormatVersion, FormatVersion)
	}
	if m.Version != o.Cfg.ImageTag {
		return refused("versione diversa: l'archivio è della versione %s, questa installazione è la %s. "+
			"Il restore funziona solo sulla stessa versione: reinstalla la versione %s e riprova", m.Version, o.Cfg.ImageTag, m.Version)
	}

	dest := o.dest()
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return err
	}
	// Lavoro nella cartella dei backup, non in /tmp (spesso in RAM).
	stage, err := os.MkdirTemp(dest, ".restore-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err := os.Chmod(stage, 0o700); err != nil {
		return err
	}
	if err := extractVerified(tr, &m, stage); err != nil {
		return err
	}
	has := map[string]bool{}
	for _, f := range m.Files {
		has[f.Name] = true
	}
	for _, need := range []string{DatabaseName, GitDataName, SecretsName} {
		if !has[need] {
			return refused("archivio incompleto: manca %s", need)
		}
	}
	o.logf("archivio verificato: versione %s del %s, %d file", m.Version, m.CreatedAt.Format(time.RFC3339), len(m.Files))

	gitPath, err := o.Cluster.VolumePath(ctx, o.Cfg.Release+"-git-data")
	if err != nil {
		return fmt.Errorf("volume dei repo (git-data): %w", err)
	}
	attPath := ""
	if has[AttachmentsName] {
		if attPath, err = o.Cluster.VolumePath(ctx, o.Cfg.Release+"-attachments-data"); err != nil {
			return fmt.Errorf("volume degli allegati: %w", err)
		}
	}

	// Repliche di prima, poi stop.
	saved := map[string]int{}
	for _, c := range restoreStops {
		n, found, e := o.Cluster.Replicas(ctx, o.deployment(c))
		if e != nil {
			return fmt.Errorf("repliche di %s: %w", c, e)
		}
		if found && n > 0 {
			saved[c] = n
		}
	}
	scaleBack := func() error {
		var errs []error
		for _, c := range restoreStops {
			if n, ok := saved[c]; ok {
				if e := o.Cluster.Scale(ctx, o.deployment(c), n); e != nil {
					errs = append(errs, fmt.Errorf("riavvio di %s: %w", c, e))
				}
			}
		}
		return errors.Join(errs...)
	}
	failed := func(e error) error {
		return fmt.Errorf("%w\nI servizi sono rimasti FERMI per non servire dati a metà: dopo aver risolto, rilancia il restore "+
			"(o riaccendili con: kubectl -n %s scale deployment %s-gateway %s-git %s-core %s-identity --replicas=1)",
			e, o.Cfg.Namespace, o.Cfg.Release, o.Cfg.Release, o.Cfg.Release, o.Cfg.Release)
	}
	o.logf("fermo i servizi: %s", strings.Join(sortedKeys(saved), ", "))
	for _, c := range restoreStops {
		if _, ok := saved[c]; ok {
			if e := o.Cluster.Scale(ctx, o.deployment(c), 0); e != nil {
				return failed(fmt.Errorf("arresto di %s: %w", c, e))
			}
		}
	}
	for _, c := range restoreStops {
		if _, ok := saved[c]; ok {
			if e := o.Cluster.WaitStopped(ctx, c); e != nil {
				return failed(fmt.Errorf("attesa dell'arresto di %s: %w", c, e))
			}
		}
	}

	o.logf("ripristino del database (in una sola transazione)")
	dump, err := os.Open(filepath.Join(stage, DatabaseName))
	if err != nil {
		return err
	}
	defer func() { _ = dump.Close() }()
	if e := o.Cluster.PGExec(ctx, io.MultiReader(strings.NewReader(restorePrelude), dump), io.Discard, "sh", "-c", psqlCmd); e != nil {
		return failed(fmt.Errorf("ripristino del database: %w", e))
	}

	o.logf("ripristino dei repo (git-data)")
	if e := restoreVolume(filepath.Join(stage, GitDataName), gitPath); e != nil {
		return failed(fmt.Errorf("ripristino di git-data: %w", e))
	}
	if attPath != "" {
		o.logf("ripristino degli allegati")
		if e := restoreVolume(filepath.Join(stage, AttachmentsName), attPath); e != nil {
			return failed(fmt.Errorf("ripristino degli allegati: %w", e))
		}
	}

	o.logf("ripristino dei Secret")
	if e := o.restoreSecrets(ctx, filepath.Join(stage, SecretsName)); e != nil {
		return failed(e)
	}
	n, err := o.restoreConfig(stage, &m)
	if err != nil {
		return failed(fmt.Errorf("ripristino della configurazione: %w", err))
	}
	o.logf("ripristinati %d file di configurazione (config.yaml resta quello della nuova installazione)", n)

	// CA e certificato ripristinati su disco vanno anche nel cluster. Se
	// fallisce, i dati sono già a posto: i servizi si riaccendono comunque e
	// l'errore (alla fine) dice come rifare solo questo passo.
	var tlsErr error
	if o.PublishTLS != nil {
		o.logf("riallineo CA e certificato nel cluster (Secret %s-tls, ConfigMap %s-ca)", o.Cfg.Release, o.Cfg.Release)
		if e := o.PublishTLS(ctx); e != nil {
			tlsErr = fmt.Errorf("i dati sono ripristinati ma CA e certificato non sono stati pubblicati nel cluster: %w\n"+
				"I servizi sono stati riaccesi; rilancia: sudo gitstack-tls ensure", e)
		}
	}

	o.logf("riavvio i servizi")
	if e := scaleBack(); e != nil {
		return errors.Join(tlsErr, failed(e))
	}
	for _, c := range restoreStops {
		if _, ok := saved[c]; ok {
			if e := o.Cluster.WaitReady(ctx, o.deployment(c)); e != nil {
				return errors.Join(tlsErr, fmt.Errorf("%s non è tornato pronto dopo il restore: %w", c, e))
			}
		}
	}
	if o.PublishTLS != nil && tlsErr == nil {
		// Il web monta il ConfigMap della CA come cartella: il kubelet lo
		// aggiorna dopo circa un minuto. Il restart fa sì che
		// /downloads/ca.crt sia già giusto quando il comando esce.
		web := o.deployment("web")
		if _, found, e := o.Cluster.Replicas(ctx, web); e != nil {
			return fmt.Errorf("repliche di web: %w", e)
		} else if found {
			o.logf("riavvio il web per servire la CA ripristinata")
			if e := o.Cluster.Restart(ctx, web); e != nil {
				return fmt.Errorf("riavvio di web: %w (la CA è nel ConfigMap; il web la serve entro un minuto)", e)
			}
			if e := o.Cluster.WaitReady(ctx, web); e != nil {
				return fmt.Errorf("web non è tornato pronto dopo il riavvio: %w", e)
			}
		}
	}
	return tlsErr
}

// extractVerified estrae le voci rimanenti in stage verificando che siano
// elencate nel manifest e che dimensione e SHA-256 combacino.
func extractVerified(tr *tar.Reader, m *Manifest, stage string) error {
	want := map[string]FileSum{}
	for _, f := range m.Files {
		want[f.Name] = f
	}
	seen := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return refused("archivio danneggiato: %v", err)
		}
		f, ok := want[hdr.Name]
		if !ok {
			return refused("archivio non valido: la voce %q non è nel manifest", hdr.Name)
		}
		dst, err := safeJoin(stage, hdr.Name)
		if err != nil {
			return refused("%v", err)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		h := sha256.New()
		n, cerr := io.Copy(io.MultiWriter(out, h), tr)
		if e := out.Close(); cerr == nil {
			cerr = e
		}
		if cerr != nil {
			return refused("archivio danneggiato leggendo %s: %v", hdr.Name, cerr)
		}
		if n != f.Size || hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
			return refused("checksum errato per %s: archivio corrotto o alterato", hdr.Name)
		}
		seen[hdr.Name] = true
	}
	for name := range want {
		if !seen[name] {
			return refused("archivio incompleto: manca %s", name)
		}
	}
	return nil
}

func restoreVolume(tarPath, dst string) error {
	if err := emptyDir(dst); err != nil {
		return err
	}
	f, err := os.Open(tarPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return untarDir(f, dst)
}

func (o *Options) restoreSecrets(ctx context.Context, path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var secrets []Secret
	if err := json.Unmarshal(b, &secrets); err != nil {
		return refused("secrets.json non valido: %v", err)
	}
	skip := o.Cfg.Release + "-postgres"
	for _, s := range secrets {
		if s.Name == skip {
			continue
		}
		if err := o.Cluster.ApplySecret(ctx, s); err != nil {
			return fmt.Errorf("ripristino di un Secret: %w", err)
		}
	}
	return nil
}

// keepOnRestore: file (relativi a ConfigDir) che descrivono la macchina di
// oggi, non quella del backup, e restano quelli della nuova installazione:
// tls.conf (nomi e IP per i SAN, modalità, release) e install.conf (host).
// Così `gitstack-tls ensure` riemette il certificato per l'host attuale, ma
// firmato dalla CA ripristinata.
var keepOnRestore = map[string]bool{"tls/tls.conf": true, "tls/install.conf": true}

// restoreConfig rimette i file di config/ in ConfigDir, tranne il file di
// configurazione in uso. Ritorna quanti ne ha scritti.
func (o *Options) restoreConfig(stage string, m *Manifest) (int, error) {
	if o.ConfigDir == "" {
		return 0, nil
	}
	skip := ""
	if o.ConfigFile != "" {
		if rel, err := filepath.Rel(o.ConfigDir, o.ConfigFile); err == nil {
			skip = filepath.ToSlash(rel)
		}
	}
	n := 0
	for _, f := range m.Files {
		if !strings.HasPrefix(f.Name, ConfigPrefix) {
			continue
		}
		rel := strings.TrimPrefix(f.Name, ConfigPrefix)
		if rel == skip || keepOnRestore[rel] {
			continue
		}
		dst, err := safeJoin(o.ConfigDir, rel)
		if err != nil {
			return n, err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return n, err
		}
		data, err := os.ReadFile(filepath.Join(stage, filepath.FromSlash(f.Name)))
		if err != nil {
			return n, err
		}
		if err := os.WriteFile(dst, data, 0o600); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
