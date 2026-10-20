// Comando ci-runner: il processo del pod di un job (ADR 0001). Parla runner.v1 col
// ci-gateway, esegue un solo job con act e riporta l'esito; poi esce.
//
// Variabili:
//
//	GITSTACK_CI_GATEWAY_URL     indirizzo del ci-gateway (obbligatoria)
//	GITSTACK_CI_RUNNER_SECRET   credenziale dei runner interni (obbligatoria)
//	GITSTACK_CI_RUNNER_NAME     nome mostrato come runner del job (default: hostname)
//	GITSTACK_CI_WORKDIR         cartella di lavoro (default /work)
//	GITSTACK_CI_ACT             binario di act (default act)
//	GITSTACK_CI_FETCH_TIMEOUT   attesa di un job, es. 2m (default 2m)
//
// Uscita: 0 se il job è stato eseguito e l'esito riportato (qualunque esito), 3 se non
// c'era nessun job, 1 per ogni altro errore.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/cirunner"
)

func main() { os.Exit(run()) }

func run() int {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "ci-runner")
	// Prima di leggere il segreto: un passo del workflow (stesso uid) non deve poter leggere /proc/<pid>/environ.
	if err := hardenProcess(); err != nil {
		log.Error("protezione del processo non riuscita", "err", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := cirunner.Config{
		GatewayURL: os.Getenv("GITSTACK_CI_GATEWAY_URL"),
		Secret:     os.Getenv("GITSTACK_CI_RUNNER_SECRET"),
		Name:       os.Getenv("GITSTACK_CI_RUNNER_NAME"),
		WorkDir:    os.Getenv("GITSTACK_CI_WORKDIR"),
		ActBin:     os.Getenv("GITSTACK_CI_ACT"),
		Log:        log,
	}
	if cfg.WorkDir == "" {
		cfg.WorkDir = "/work"
	}
	if v := os.Getenv("GITSTACK_CI_FETCH_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			log.Error("GITSTACK_CI_FETCH_TIMEOUT non valida", "value", v)
			return 1
		}
		cfg.FetchTimeout = d
	}
	res, err := cirunner.Run(ctx, cfg)
	switch {
	case errors.Is(err, cirunner.ErrNoTask):
		log.Info("nessun job in coda")
		return 3
	case err != nil:
		log.Error("job non eseguito", "task", res.TaskID, "err", err)
		return 1
	}
	log.Info("job eseguito", "task", res.TaskID, "job", res.Job, "result", fmt.Sprint(res.Conclusion))
	return 0
}
