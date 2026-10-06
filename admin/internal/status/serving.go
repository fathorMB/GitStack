package status

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/fathorMB/GitStack/admin/internal/config"
)

// ServesCA è vero se l'installazione pubblica la CA su /downloads/ca.crt:
// solo con TLS internal o custom (con insecure e letsencrypt non c'è).
func ServesCA(cfg *config.Config) bool {
	return cfg.TLS == "internal" || cfg.TLS == "custom"
}

// CheckServing fa un solo giro di verifica attraverso l'Ingress: /api/healthz
// deve dare 200 e, se ServesCA, anche /downloads/ca.crt (chiesto in http://,
// come fa l'e2e: il redirect a https lo lascia passare) con un PEM
// CERTIFICATE nel corpo. Il client è quello di status se client è nil.
// L'errore dice quale URL ha fallito e con quale stato.
func CheckServing(ctx context.Context, cfg *config.Config, client *http.Client) error {
	if client == nil {
		var err error
		if client, err = newAPIClient(cfg); err != nil {
			return err
		}
	}
	scheme := "http"
	if cfg.TLS != "" && cfg.TLS != "insecure" {
		scheme = "https"
	}
	if err := getOK(ctx, client, scheme+"://"+cfg.Host+"/api/healthz", false); err != nil {
		return err
	}
	if ServesCA(cfg) {
		// La CA va chiesta in http: il client non deve fidarsi di nulla per
		// scaricarla, e non dipende dal certificato appena riallineato.
		plain := &http.Client{Timeout: client.Timeout}
		if err := getOK(ctx, plain, "http://"+cfg.Host+"/downloads/ca.crt", true); err != nil {
			return err
		}
	}
	return nil
}

// CheckCA verifica solo /downloads/ca.crt (se ServesCA).
func CheckCA(ctx context.Context, cfg *config.Config, client *http.Client) error {
	if !ServesCA(cfg) {
		return nil
	}
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return getOK(ctx, client, "http://"+cfg.Host+"/downloads/ca.crt", true)
}

func getOK(ctx context.Context, client *http.Client, url string, wantPEM bool) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("%s: %v", url, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	if wantPEM && !bytes.Contains(body, []byte("-----BEGIN CERTIFICATE-----")) {
		return fmt.Errorf("%s: HTTP 200 ma il corpo non è un certificato PEM", url)
	}
	return nil
}
