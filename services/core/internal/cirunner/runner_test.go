package cirunner_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	runnerv1 "code.gitea.io/actions-proto-go/runner/v1"
	"code.gitea.io/actions-proto-go/runner/v1/runnerv1connect"
	"connectrpc.com/connect"
	"github.com/fathorMB/GitStack/services/core/internal/cirunner"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	secret   = "segreto-runner"
	jobToken = "gsj_token-di-prova"
)

// TestMain: con CIRUNNER_FAKE_ACT=1 il binario di test fa da `act` finto, così il test
// gira ovunque (anche su Windows, senza act né sh).
func TestMain(m *testing.M) {
	if os.Getenv("CIRUNNER_FAKE_ACT") == "1" {
		os.Exit(fakeAct(os.Args[1:]))
	}
	os.Exit(m.Run())
}

var runRe = regexp.MustCompile(`(?m)^\s*- run: (.*)$`)

// fakeAct legge il workflow indicato da -W e stampa l'output nello stile di act: un passo
// per ogni `- run:`; un comando che contiene "exit 1" fallisce e ferma il job.
func fakeAct(args []string) int {
	var wf string
	for i, a := range args {
		if a == "-W" && i+1 < len(args) {
			wf = args[i+1]
		}
	}
	b, err := os.ReadFile(wf)
	if err != nil {
		fmt.Println("Error:", err)
		return 1
	}
	if os.Getenv("GITHUB_TOKEN") != jobToken {
		fmt.Println("Error: GITHUB_TOKEN assente")
		return 1
	}
	if _, err := os.Stat("README.md"); err != nil { // il codice scaricato è nella cartella corrente
		fmt.Println("Error: workspace vuoto")
		return 1
	}
	fmt.Println("[wf/build]   ✅  Success - Set up job")
	for i, m := range runRe.FindAllStringSubmatch(string(b), -1) {
		fmt.Printf("[wf/build] ⭐ Run Main Passo %d\n", i+1)
		fmt.Printf("[wf/build]   | eseguo %s\n", m[1])
		if strings.Contains(m[1], "exit 1") {
			fmt.Println("[wf/build]   ❌  Failure - Main set -e")
			fmt.Printf("[wf/build]   ❌  Failure - Main Passo %d [1.5ms]\n", i+1)
			fmt.Println("[wf/build] 🏁  Job failed")
			return 1
		}
		fmt.Printf("[wf/build]   ✅  Success - Main Passo %d [1.5ms]\n", i+1)
	}
	fmt.Println("[wf/build] 🏁  Job succeeded")
	return 0
}

// fakeGateway è un ci-gateway finto: consegna un solo task e registra ciò che il runner riporta.
type fakeGateway struct {
	runnerv1connect.UnimplementedRunnerServiceHandler
	mu       sync.Mutex
	workflow string
	taken    bool
	rows     []string
	result   runnerv1.Result
	steps    []*runnerv1.StepState
	archive  []byte
	tokens   []string
}

func (g *fakeGateway) FetchTask(_ context.Context, req *connect.Request[runnerv1.FetchTaskRequest]) (*connect.Response[runnerv1.FetchTaskResponse], error) {
	if req.Header().Get("x-runner-token") != secret {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no"))
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.taken {
		return connect.NewResponse(&runnerv1.FetchTaskResponse{}), nil
	}
	g.taken = true
	cx, _ := structpb.NewStruct(map[string]any{"job": "build", "event_name": "push", "sha": strings.Repeat("a", 40)})
	return connect.NewResponse(&runnerv1.FetchTaskResponse{Task: &runnerv1.Task{
		Id: 42, WorkflowPayload: []byte(g.workflow), Context: cx, Secrets: map[string]string{"GITHUB_TOKEN": jobToken},
	}}), nil
}

func (g *fakeGateway) check(h interface{ Get(string) string }) error {
	g.mu.Lock()
	g.tokens = append(g.tokens, h.Get("x-runner-token"))
	g.mu.Unlock()
	if h.Get("x-runner-token") != jobToken {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("token"))
	}
	return nil
}

func (g *fakeGateway) UpdateLog(_ context.Context, req *connect.Request[runnerv1.UpdateLogRequest]) (*connect.Response[runnerv1.UpdateLogResponse], error) {
	if err := g.check(req.Header()); err != nil {
		return nil, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	idx := req.Msg.GetIndex()
	if idx > int64(len(g.rows)) {
		return connect.NewResponse(&runnerv1.UpdateLogResponse{AckIndex: int64(len(g.rows))}), nil
	}
	for i, r := range req.Msg.GetRows() {
		if int(idx)+i >= len(g.rows) {
			g.rows = append(g.rows, r.GetContent())
		}
	}
	return connect.NewResponse(&runnerv1.UpdateLogResponse{AckIndex: int64(len(g.rows))}), nil
}

func (g *fakeGateway) UpdateTask(_ context.Context, req *connect.Request[runnerv1.UpdateTaskRequest]) (*connect.Response[runnerv1.UpdateTaskResponse], error) {
	if err := g.check(req.Header()); err != nil {
		return nil, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	st := req.Msg.GetState()
	if st.GetId() != 42 {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("task"))
	}
	g.steps = st.GetSteps()
	if st.GetResult() != runnerv1.Result_RESULT_UNSPECIFIED {
		g.result = st.GetResult()
	}
	return connect.NewResponse(&runnerv1.UpdateTaskResponse{}), nil
}

func tarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	_ = tw.WriteHeader(&tar.Header{Name: "workspace/", Typeflag: tar.TypeDir, Mode: 0o755})
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: "workspace/" + name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(body))
	}
	// Un collegamento simbolico verso l'esterno non deve essere creato.
	_ = tw.WriteHeader(&tar.Header{Name: "workspace/evil", Typeflag: tar.TypeSymlink, Linkname: "../../fuori", Mode: 0o777})
	_ = tw.Close()
	_ = zw.Close()
	return buf.Bytes()
}

func start(t *testing.T, g *fakeGateway) string {
	t.Helper()
	mux := http.NewServeMux()
	path, h := runnerv1connect.NewRunnerServiceHandler(g)
	mux.Handle(path, h)
	mux.HandleFunc("GET /ci/v1/archive", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+jobToken {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write(g.archive)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func cfgFor(t *testing.T, url string) cirunner.Config {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CIRUNNER_FAKE_ACT", "1")
	return cirunner.Config{GatewayURL: url, Secret: secret, Name: "pod-test", ActBin: exe, WorkDir: t.TempDir(),
		FetchTimeout: 3 * time.Second, FlushEvery: 20 * time.Millisecond}
}

const twoSteps = "name: due\non: push\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo uno\n      - run: echo due\n"

// TestRun_WorkflowADuePassi: il runner prende il task, scarica il codice, esegue il workflow
// a due passi e riporta log, passi ed esito.
func TestRun_WorkflowADuePassi(t *testing.T) {
	g := &fakeGateway{workflow: twoSteps, archive: tarball(t, map[string]string{"README.md": "ciao", "src/a.txt": "a"})}
	cfg := cfgFor(t, start(t, g))
	res, err := cirunner.Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.TaskID != 42 || res.Job != "build" || res.Conclusion != runnerv1.Result_RESULT_SUCCESS {
		t.Fatalf("esito: %+v", res)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.result != runnerv1.Result_RESULT_SUCCESS {
		t.Errorf("esito riportato: %v", g.result)
	}
	if len(g.steps) != 2 {
		t.Fatalf("passi: %v", g.steps)
	}
	for i, s := range g.steps {
		if s.GetId() != int64(i) || s.GetResult() != runnerv1.Result_RESULT_SUCCESS || s.GetStartedAt() == nil || s.GetStoppedAt() == nil {
			t.Errorf("passo %d: %v", i, s)
		}
	}
	log := strings.Join(g.rows, "\n")
	for _, want := range []string{"Run Main Passo 1", "| eseguo echo uno", "Success - Main Passo 2", "Job succeeded"} {
		if !strings.Contains(log, want) {
			t.Errorf("manca %q nel log:\n%s", want, log)
		}
	}
	if strings.Index(log, "Passo 1") > strings.Index(log, "Passo 2") {
		t.Error("log fuori ordine")
	}
	for _, tok := range g.tokens {
		if tok != jobToken {
			t.Errorf("update con un token diverso da quello del job: %q", tok)
		}
	}
	if _, err := os.Lstat(filepath.Join(cfg.WorkDir, "workspace", "evil")); err == nil {
		t.Error("il collegamento simbolico è stato creato")
	}
	if b, err := os.ReadFile(filepath.Join(cfg.WorkDir, "workspace", "src", "a.txt")); err != nil || string(b) != "a" {
		t.Errorf("file estratto: %q %v", b, err)
	}
}

// TestRun_PassoFallito: un passo che fallisce chiude il job con failure e il passo risulta fallito.
func TestRun_PassoFallito(t *testing.T) {
	g := &fakeGateway{workflow: strings.Replace(twoSteps, "echo due", "exit 1", 1), archive: tarball(t, map[string]string{"README.md": "x"})}
	res, err := cirunner.Run(context.Background(), cfgFor(t, start(t, g)))
	if err != nil {
		t.Fatal(err)
	}
	if res.Conclusion != runnerv1.Result_RESULT_FAILURE {
		t.Fatalf("esito: %v", res.Conclusion)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.result != runnerv1.Result_RESULT_FAILURE || len(g.steps) != 2 ||
		g.steps[0].GetResult() != runnerv1.Result_RESULT_SUCCESS || g.steps[1].GetResult() != runnerv1.Result_RESULT_FAILURE {
		t.Fatalf("esito %v passi %v", g.result, g.steps)
	}
}

// TestRun_SenzaJobEcredenziale: nessun job in coda → ErrNoTask; segreto sbagliato → errore subito.
func TestRun_SenzaJobEcredenziale(t *testing.T) {
	g := &fakeGateway{workflow: twoSteps, taken: true}
	cfg := cfgFor(t, start(t, g))
	cfg.FetchTimeout = 100 * time.Millisecond
	if _, err := cirunner.Run(context.Background(), cfg); !errors.Is(err, cirunner.ErrNoTask) {
		t.Errorf("senza job: %v", err)
	}
	cfg.Secret = "sbagliato"
	if _, err := cirunner.Run(context.Background(), cfg); err == nil || errors.Is(err, cirunner.ErrNoTask) {
		t.Errorf("segreto sbagliato: %v", err)
	}
}

// TestRun_ActVero esegue lo stesso workflow con il binario vero di act, quando c'è:
// GITSTACK_TEST_ACT=<percorso di act> (la CI lo estrae dall'immagine del runner).
func TestRun_ActVero(t *testing.T) {
	act := os.Getenv("GITSTACK_TEST_ACT")
	if act == "" {
		t.Skip("GITSTACK_TEST_ACT non impostata")
	}
	if out, err := exec.Command(act, "--version").CombinedOutput(); err != nil {
		t.Fatalf("act --version: %v %s", err, out)
	}
	g := &fakeGateway{workflow: twoSteps, archive: tarball(t, map[string]string{"README.md": "x"})}
	cfg := cfgFor(t, start(t, g))
	t.Setenv("CIRUNNER_FAKE_ACT", "")
	cfg.ActBin = act
	cfg.FetchTimeout = 5 * time.Second
	res, err := cirunner.Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if res.Conclusion != runnerv1.Result_RESULT_SUCCESS || g.result != runnerv1.Result_RESULT_SUCCESS || len(g.steps) != 2 {
		t.Fatalf("esito %v/%v passi %d\n%s", res.Conclusion, g.result, len(g.steps), strings.Join(g.rows, "\n"))
	}
}
