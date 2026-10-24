package cirunner_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
	return connect.NewResponse(&runnerv1.RegisterResponse{Runner: &runnerv1.Runner{Token: "gsr_token-del-runner", Name: req.Msg.GetName()}}), nil
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

	tok, err := cirunner.Register(context.Background(), nil, srv.URL+"/", "gsrt_buono", "box", "v1", []string{"linux"})
	if err != nil || tok != "gsr_token-del-runner" {
		t.Fatalf("tok = %q, err = %v", tok, err)
	}
	if g.got.GetName() != "box" || g.got.GetVersion() != "v1" || len(g.got.GetLabels()) != 1 || g.got.GetLabels()[0] != "linux" {
		t.Errorf("richiesta: %+v", g.got)
	}
	if _, err := cirunner.Register(context.Background(), nil, srv.URL, "gsrt_cattivo", "box", "v1", nil); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("token rifiutato: %v", err)
	}
	if _, err := cirunner.Register(context.Background(), nil, "", "", "", "", nil); err == nil {
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
