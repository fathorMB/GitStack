package apps

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

func TestBuildDeployment_CancelloDiRete(t *testing.T) {
	app := store.App{ID: uuid.New(), RepoID: uuid.New()}
	v := store.AppVersion{Number: 1}

	senza := BuildDeployment(app, v, BuildOptions{Replicas: 1})
	if n := len(senza.Spec.Template.Spec.InitContainers); n != 0 {
		t.Fatalf("senza GateImage non ci sono init container: %d", n)
	}

	d := BuildDeployment(app, v, BuildOptions{Replicas: 1, GateImage: "ghcr.io/x/gitstack-core:t"})
	init := d.Spec.Template.Spec.InitContainers
	if len(init) != 1 || init[0].Name != GateContainerName || init[0].Image != "ghcr.io/x/gitstack-core:t" {
		t.Fatalf("init container del cancello inatteso: %+v", init)
	}
	if got := init[0].Command; len(got) != 2 || got[1] != "apps-gate" {
		t.Fatalf("comando del cancello: %v", got)
	}
	sc := init[0].SecurityContext
	if sc == nil || sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot || sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
		t.Fatalf("il cancello deve girare senza privilegi: %+v", sc)
	}
	if n := len(d.Spec.Template.Spec.Containers); n != 1 {
		t.Fatalf("il container dell'app resta uno: %d", n)
	}
}

func TestRunGate(t *testing.T) {
	ctx := context.Background()
	env := func(host, port string) func(string) string {
		return func(k string) string {
			switch k {
			case "KUBERNETES_SERVICE_HOST":
				return host
			case "KUBERNETES_SERVICE_PORT":
				return port
			}
			return ""
		}
	}
	if err := RunGate(ctx, env("", ""), time.Second); err == nil {
		t.Fatal("senza indirizzo dell'API il cancello deve fallire (fail-closed)")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	// Raggiungibile: la barriera non c'è, il cancello non apre.
	start := time.Now()
	if err := RunGate(ctx, env(host, port), 600*time.Millisecond); err == nil {
		t.Fatal("API raggiungibile: il cancello deve restare chiuso")
	}
	if time.Since(start) < 500*time.Millisecond {
		t.Fatal("il cancello deve aspettare fino al timeout")
	}
	// La barriera arriva mentre il cancello aspetta: apre.
	go func() { time.Sleep(400 * time.Millisecond); _ = ln.Close() }()
	if err := RunGate(ctx, env(host, port), 5*time.Second); err != nil {
		t.Fatalf("API chiusa durante l'attesa: il cancello deve aprire: %v", err)
	}
}

// GIT-426: l'errore di dial arrivato alla scadenza dell'attesa (ctx non ancora annullato) non è una barriera.
func TestDialSaysUnreachable(t *testing.T) {
	ctx := context.Background()
	deadline := time.Now().Add(time.Second)
	errDial := errors.New("dial tcp: i/o timeout")
	if !dialSaysUnreachable(ctx, errDial, deadline, deadline.Add(-time.Millisecond)) {
		t.Fatal("errore prima della scadenza: barriera presente")
	}
	if dialSaysUnreachable(ctx, errDial, deadline, deadline) {
		t.Fatal("errore alla scadenza: non è una barriera, il cancello resta chiuso")
	}
	if dialSaysUnreachable(ctx, errDial, deadline, deadline.Add(time.Millisecond)) {
		t.Fatal("errore dopo la scadenza: il cancello resta chiuso")
	}
	if dialSaysUnreachable(ctx, nil, deadline, deadline.Add(-time.Millisecond)) {
		t.Fatal("dial riuscito: API raggiungibile")
	}
}

func TestGateTimeoutFromEnv(t *testing.T) {
	get := func(v string) func(string) string {
		return func(k string) string {
			if k == EnvGateTimeout {
				return v
			}
			return ""
		}
	}
	for v, want := range map[string]time.Duration{"": GateTimeout, "x": GateTimeout, "0": GateTimeout, "-3": GateTimeout, "5": 5 * time.Second} {
		if got := GateTimeoutFromEnv(get(v)); got != want {
			t.Errorf("%q: %s, atteso %s", v, got, want)
		}
	}
}
