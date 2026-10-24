package cirunner

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	runnerv1 "code.gitea.io/actions-proto-go/runner/v1"
	"code.gitea.io/actions-proto-go/runner/v1/runnerv1connect"
	"connectrpc.com/connect"
)

// Runner esterno (GIT-278, ADR 0001 §4): lo stesso Run del pod interno, in un ciclo, con il
// token del runner ottenuto registrandosi con un token monouso dell'amministratore.

// Register registra un runner sul ci-gateway e ritorna il token del runner e le etichette
// come le ha salvate il server (con «self-hosted», normalizzate). Il token di registrazione
// non vale più dopo questa chiamata.
func Register(ctx context.Context, hc *http.Client, gatewayURL, regToken, name, version string, labels []string) (string, []string, error) {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	gatewayURL = strings.TrimRight(gatewayURL, "/")
	if gatewayURL == "" || regToken == "" || name == "" {
		return "", nil, errors.New("servono l'indirizzo del gateway, il token di registrazione e il nome")
	}
	client := runnerv1connect.NewRunnerServiceClient(hc, gatewayURL)
	res, err := client.Register(ctx, connect.NewRequest(&runnerv1.RegisterRequest{
		Name: name, Token: regToken, Version: version, Labels: labels}))
	if err != nil {
		return "", nil, fmt.Errorf("registrazione rifiutata: %w", err)
	}
	tok := res.Msg.GetRunner().GetToken()
	if tok == "" {
		return "", nil, errors.New("il gateway non ha restituito il token del runner")
	}
	return tok, res.Msg.GetRunner().GetLabels(), nil
}

// Serve esegue i job uno dopo l'altro finché ctx non finisce. Ritorna un errore solo se
// il gateway rifiuta il token del runner (runner revocato): gli altri errori si ritentano.
// Prima di ogni job il workspace del precedente è tolto.
func Serve(ctx context.Context, cfg Config) error {
	cfg.defaults()
	if cfg.FetchTimeout > 30*time.Second {
		cfg.FetchTimeout = 30 * time.Second
	}
	for ctx.Err() == nil {
		_ = os.RemoveAll(filepath.Join(cfg.WorkDir, "workspace"))
		_ = os.RemoveAll(filepath.Join(cfg.WorkDir, "workflow"))
		res, err := Run(ctx, cfg)
		switch {
		case errors.Is(err, ErrNoTask), ctx.Err() != nil:
			continue
		case err != nil && res.TaskID == 0 && connect.CodeOf(err) == connect.CodeUnauthenticated:
			return err
		case err != nil:
			cfg.Log.Error("job non eseguito", "task", res.TaskID, "err", err)
			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
			}
			continue
		}
		cfg.Log.Info("job eseguito", "task", res.TaskID, "job", res.Job, "result", fmt.Sprint(res.Conclusion))
	}
	return nil
}
