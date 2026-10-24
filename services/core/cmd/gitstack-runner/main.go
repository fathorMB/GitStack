// Comando gitstack-runner: il runner esterno della CI di GitStack (GIT-278, ADR 0001 §4).
// Si registra una volta con un token monouso creato dall'amministratore, poi prende i job
// il cui runs-on è coperto dalle sue etichette e li esegue con act, riportando log e stati
// al ci-gateway come il runner integrato. Il runner apre sempre la connessione (pull).
//
//	gitstack-runner register --url <ci-gateway> --token <token> [--name N] [--labels a,b]
//	gitstack-runner run [--workdir D] [--act act]
//
// Serve `act` (nektos/act) nel PATH o indicato con --act. Vedi docs/ci-runner-esterno.md.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/fathorMB/GitStack/services/core/internal/cirunner"
)

// version è scritta in build con -X main.version.
var version = "dev"

type config struct {
	URL    string   `json:"url"`
	Token  string   `json:"runnerToken"`
	Name   string   `json:"name"`
	Labels []string `json:"labels"`
}

func main() { os.Exit(run(os.Args[1:])) }

func defaultConfigPath() string {
	d, err := os.UserConfigDir()
	if err != nil {
		d = "."
	}
	return filepath.Join(d, "gitstack-runner", "config.json")
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "uso: gitstack-runner register|run|version [opzioni]")
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Println(version)
		return 0
	case "register":
		return register(args[1:])
	case "run":
		return serve(args[1:])
	}
	fmt.Fprintf(os.Stderr, "comando sconosciuto %q\n", args[0])
	return 2
}

func register(args []string) int {
	fs := flag.NewFlagSet("register", flag.ContinueOnError)
	url := fs.String("url", os.Getenv("GITSTACK_RUNNER_URL"), "indirizzo del ci-gateway")
	token := fs.String("token", os.Getenv("GITSTACK_RUNNER_REGISTRATION_TOKEN"), "token di registrazione")
	host, _ := os.Hostname()
	name := fs.String("name", host, "nome del runner")
	labels := fs.String("labels", "", "etichette separate da virgola (self-hosted c'è sempre)")
	cfgPath := fs.String("config", defaultConfigPath(), "file di configurazione da scrivere")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var ls []string
	for _, l := range strings.Split(*labels, ",") {
		if l = strings.TrimSpace(l); l != "" {
			ls = append(ls, l)
		}
	}
	tok, saved, err := cirunner.Register(ctx, nil, *url, *token, *name, version, ls)
	if err != nil {
		fmt.Fprintln(os.Stderr, "errore:", err)
		return 1
	}
	c := config{URL: strings.TrimRight(*url, "/"), Token: tok, Name: *name, Labels: saved}
	raw, _ := json.MarshalIndent(c, "", "  ")
	if err := os.MkdirAll(filepath.Dir(*cfgPath), 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "errore:", err)
		return 1
	}
	if err := os.WriteFile(*cfgPath, append(raw, '\n'), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "errore:", err)
		return 1
	}
	fmt.Printf("runner %q registrato; configurazione in %s\n", *name, *cfgPath)
	return 0
}

func serve(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	cfgPath := fs.String("config", defaultConfigPath(), "file di configurazione scritto da register")
	workdir := fs.String("workdir", "", "cartella di lavoro (default: accanto alla configurazione)")
	act := fs.String("act", "act", "binario di act")
	mirror := fs.String("action-mirror", os.Getenv("GITSTACK_CI_ACTION_MIRROR"), "prefisso opzionale del mirror delle action")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "gitstack-runner")
	raw, err := os.ReadFile(*cfgPath)
	if err != nil {
		log.Error("configurazione non leggibile: lancia prima `gitstack-runner register`", "err", err)
		return 1
	}
	var c config
	if err := json.Unmarshal(raw, &c); err != nil || c.URL == "" || c.Token == "" {
		log.Error("configurazione non valida", "path", *cfgPath)
		return 1
	}
	wd := *workdir
	if wd == "" {
		wd = filepath.Join(filepath.Dir(*cfgPath), "work")
	}
	if err := os.MkdirAll(wd, 0o700); err != nil {
		log.Error("cartella di lavoro non creabile", "err", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Info("runner avviato", "name", c.Name, "gateway", c.URL, "labels", c.Labels, "version", version)
	err = cirunner.Serve(ctx, cirunner.Config{
		GatewayURL: c.URL, Secret: c.Token, Name: c.Name, Labels: c.Labels,
		WorkDir: wd, ActBin: *act, ActionMirror: *mirror, Log: log,
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return 0
		}
		log.Error("il gateway ha rifiutato il token del runner (revocato?): mi fermo", "err", err)
		return 1
	}
	return 0
}
