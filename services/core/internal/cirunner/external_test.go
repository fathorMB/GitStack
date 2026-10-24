package cirunner_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	runnerv1 "code.gitea.io/actions-proto-go/runner/v1"
	"code.gitea.io/actions-proto-go/runner/v1/runnerv1connect"
	"connectrpc.com/connect"
	"github.com/fathorMB/GitStack/services/core/internal/cirunner"
)

type registerGateway struct {
	runnerv1connect.UnimplementedRunnerServiceHandler
	got *runnerv1.RegisterRequest
}

func (g *registerGateway) Register(_ context.Context, req *connect.Request[runnerv1.RegisterRequest]) (*connect.Response[runnerv1.RegisterResponse], error) {
	g.got = req.Msg
	if req.Msg.GetToken() != "gsrt_buono" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("token"))
	}
	return connect.NewResponse(&runnerv1.RegisterResponse{Runner: &runnerv1.Runner{Token: "gsr_token-del-runner", Name: req.Msg.GetName(), Labels: append([]string{"self-hosted"}, req.Msg.GetLabels()...)}}), nil
}

// TestRegister: manda nome, versione ed etichette, ritorna il token del runner; un token
// di registrazione rifiutato è un errore.
func TestRegister(t *testing.T) {
	g := &registerGateway{}
	mux := http.NewServeMux()
	path, h := runnerv1connect.NewRunnerServiceHandler(g)
	mux.Handle(path, h)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	tok, labels, err := cirunner.Register(context.Background(), nil, srv.URL+"/", "gsrt_buono", "box", "v1", []string{"linux"})
	if err != nil || tok != "gsr_token-del-runner" {
		t.Fatalf("tok = %q, err = %v", tok, err)
	}
	if len(labels) != 2 || labels[0] != "self-hosted" || labels[1] != "linux" {
		t.Errorf("etichette del server: %v", labels)
	}
	if g.got.GetName() != "box" || g.got.GetVersion() != "v1" || len(g.got.GetLabels()) != 1 || g.got.GetLabels()[0] != "linux" {
		t.Errorf("richiesta: %+v", g.got)
	}
	if _, _, err := cirunner.Register(context.Background(), nil, srv.URL, "gsrt_cattivo", "box", "v1", nil); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("token rifiutato: %v", err)
	}
	if _, _, err := cirunner.Register(context.Background(), nil, "", "", "", "", nil); err == nil {
		t.Error("senza parametri: atteso un errore")
	}
}

// TestServe_EseguePiuGiroEsceSeRevocato: Serve esegue il job e continua ad aspettare finché
// ctx non finisce (nil); se il gateway rifiuta il token del runner si ferma con l'errore.
func TestServe_EseguePiuGiroEsceSeRevocato(t *testing.T) {
	g := &fakeGateway{workflow: twoSteps, archive: tarball(t, map[string]string{"README.md": "x"})}
	cfg := cfgFor(t, start(t, g))
	cfg.FetchTimeout = 100 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- cirunner.Serve(ctx, cfg) }()
	deadline := time.Now().Add(20 * time.Second)
	for {
		g.mu.Lock()
		res := g.result
		g.mu.Unlock()
		if res == runnerv1.Result_RESULT_SUCCESS {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job non eseguito")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve a ctx finito: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve non si ferma")
	}

	cfg2 := cfgFor(t, start(t, &fakeGateway{workflow: twoSteps}))
	cfg2.Secret = "revocato"
	if err := cirunner.Serve(context.Background(), cfg2); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("token rifiutato: %v", err)
	}
}

// runArgs esegue un job con l'act finto e ritorna gli argomenti con cui è stato lanciato.
func runArgs(t *testing.T, runsOn string, labels []string) []string {
	t.Helper()
	wf := "name: x\non: push\njobs:\n  build:\n    runs-on: " + runsOn + "\n    steps:\n      - run: echo uno\n"
	g := &fakeGateway{workflow: wf, archive: tarball(t, map[string]string{"README.md": "x"})}
	cfg := cfgFor(t, start(t, g))
	cfg.Labels = labels
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	t.Setenv("CIRUNNER_FAKE_ACT_ARGS", argsFile)
	if _, err := cirunner.Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(string(raw), "\n")
}

func hasPlatform(args []string, p string) bool {
	for i, a := range args {
		if a == "-P" && i+1 < len(args) && args[i+1] == p+"=-self-hosted" {
			return true
		}
	}
	return false
}

// TestRun_PiattaformeDiActDelRunnerEsterno: act deve conoscere «self-hosted» e le etichette
// normalizzate del runner, o salta il job ed esce 0 (job verde senza passi). Un pod interno
// (nessuna etichetta) ha solo le ubuntu-*.
func TestRun_PiattaformeDiActDelRunnerEsterno(t *testing.T) {
	for _, runsOn := range []string{"self-hosted", "[self-hosted, Linux]"} {
		args := runArgs(t, runsOn, []string{"Linux:host"})
		for _, p := range []string{"self-hosted", "linux", "ubuntu-latest"} {
			if !hasPlatform(args, p) {
				t.Errorf("runs-on %s: manca -P %s=-self-hosted in %v", runsOn, p, args)
			}
		}
		if hasPlatform(args, "linux:host") || hasPlatform(args, "Linux:host") {
			t.Errorf("etichetta non normalizzata in %v", args)
		}
	}
	args := runArgs(t, "ubuntu-latest", nil)
	n := 0
	for _, a := range args {
		if a == "-P" {
			n++
		}
	}
	if n != 4 || hasPlatform(args, "self-hosted") {
		t.Errorf("pod interno: %d piattaforme, self-hosted=%v: %v", n, hasPlatform(args, "self-hosted"), args)
	}
}
