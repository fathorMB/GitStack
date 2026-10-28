package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
)

// Archivio ACME di Traefik nel backup e nel restore (v3-c, GIT-462, ADR 0002). Il resolver Let's
// Encrypt scrive account e certificati in acme.json, su un volume persistente nel namespace di Traefik
// (HelmChartConfig scritta dall'installer). Senza quell'archivio, dopo un restore Traefik dovrebbe
// riemettere tutti i certificati (con il rischio dei limiti di Let's Encrypt). Entra nell'archivio come
// traefik-acme.tar, con i permessi dei file (acme.json vuole 0600) e solo se la cartella esiste.
// I domini propri stanno nel database; i certificati del cliente stanno nel database con la chiave
// cifrata (keyring di core, nei Secret): core riscrive i Secret TLS nel namespace del TLSStore.

// ACMEName è la voce dell'archivio con la cartella ACME di Traefik.
const ACMEName = "traefik-acme.tar"

// TraefikCluster è la parte di Cluster che serve all'archivio ACME. KubectlCluster la realizza; un
// Cluster che non la realizza non salva né ripristina l'archivio.
type TraefikCluster interface {
	// ACMEPath è la cartella sull'host del volume di Traefik (ErrNotFound se non c'è: Let's Encrypt
	// non è acceso, o ErrNotBound se Traefik non l'ha mai usato).
	ACMEPath(ctx context.Context) (string, error)
	// StopTraefik porta Traefik a zero repliche e aspetta i pod; ritorna le repliche di prima
	// (0 se Traefik non c'è).
	StopTraefik(ctx context.Context) (int, error)
	// StartTraefik riporta Traefik a n repliche e aspetta che sia pronto (n=0: niente da fare).
	StartTraefik(ctx context.Context, n int) error
}

// locateACME trova la cartella ACME di Traefik. Non trovarla non è un errore: ritorna ""
func (o *Options) locateACME(ctx context.Context) (string, error) {
	tc, ok := o.Cluster.(TraefikCluster)
	if !ok {
		return "", nil
	}
	p, err := tc.ACMEPath(ctx)
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrNotBound):
		o.logf("archivio ACME di Traefik assente (Let's Encrypt non acceso): non incluso")
		return "", nil
	case err != nil:
		return "", fmt.Errorf("archivio ACME di Traefik: %w", err)
	}
	return p, nil
}

func (o *Options) stageACME(st *stager, path string) error {
	if path == "" {
		return nil
	}
	o.logf("copia dell'archivio ACME di Traefik (certificati Let's Encrypt)")
	if e := st.write(ACMEName, func(w io.Writer) error { return tarDir(w, path) }); e != nil {
		return fmt.Errorf("copia dell'archivio ACME: %w", e)
	}
	return nil
}

// restoreACME rimette l'archivio nel volume di Traefik, fermo durante la scrittura (altrimenti
// riscriverebbe acme.json con il suo stato). Se questa installazione non ha il volume ACME l'archivio
// si salta con un avviso: Traefik riemetterà i certificati.
func (o *Options) restoreACME(ctx context.Context, stage string) (err error) {
	tc, ok := o.Cluster.(TraefikCluster)
	if !ok {
		o.logf("questo cluster non sa ripristinare l'archivio ACME: non ripristinato")
		return nil
	}
	p, perr := tc.ACMEPath(ctx)
	switch {
	case errors.Is(perr, ErrNotFound), errors.Is(perr, ErrNotBound):
		o.logf("ATTENZIONE: l'archivio ha i certificati ACME ma questa installazione non ha il volume di Traefik (installa con --tls letsencrypt o con le app accese e --tls-email): non ripristinati, Traefik li riemetterà")
		return nil
	case perr != nil:
		return fmt.Errorf("archivio ACME di Traefik: %w", perr)
	}
	prev, serr := tc.StopTraefik(ctx)
	if serr != nil {
		return fmt.Errorf("arresto di Traefik: %w", serr)
	}
	defer func() {
		if e := tc.StartTraefik(context.WithoutCancel(ctx), prev); e != nil {
			err = errors.Join(err, fmt.Errorf("riavvio di Traefik: %w", e))
		}
	}()
	o.logf("ripristino dell'archivio ACME di Traefik")
	if e := restoreVolume(filepath.Join(stage, ACMEName), p); e != nil {
		return fmt.Errorf("ripristino dell'archivio ACME: %w", e)
	}
	return nil
}
