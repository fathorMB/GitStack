//go:build integration

package cigateway_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/fathorMB/GitStack/services/core/internal/cigateway"
)

const actRunnerWF = `name: act
on: push
jobs:
  prova:
    runs-on: self-hosted
    outputs:
      digest: ${{ steps.b.outputs.digest }}
    steps:
      - run: echo ACT-RUNNER-PRIMO
      - run: echo "ACT-RUNNER-SECONDO job=$GITHUB_JOB"
      - id: b
        run: echo "digest=sha256:compat" >> "$GITHUB_OUTPUT"
`

// TestActRunner_Compat (GIT-329, ADR 0001 §4): il runner di Gitea (ex act_runner) parla runner.v1
// come il nostro gateway. Registra il binario pinnato con il token monouso, prende un job
// `runs-on: self-hosted`, lo esegue in modalità host e il gateway ne conserva log ed esito.
//
// Gira solo con GITSTACK_ACT_RUNNER_BIN (percorso del binario Linux). Con GITSTACK_ACT_RUNNER_DOCKER=<immagine> il binario gira in un
// container (utile da Windows/macOS); senza, direttamente sull'host Linux. Stampa una riga
// ok/KO per controllo.
func TestActRunner_Compat(t *testing.T) {
	bin := os.Getenv("GITSTACK_ACT_RUNNER_BIN")
	if bin == "" {
		t.Skip("GITSTACK_ACT_RUNNER_BIN non impostata: prova di compatibilità con il runner di Gitea saltata")
	}
	bin, err := filepath.Abs(bin)
	if err != nil {
		t.Fatal(err)
	}
	dockerImage := os.Getenv("GITSTACK_ACT_RUNNER_DOCKER")

	e := newEnv(t)
	// Il gateway ascolta su tutte le interfacce: dal container si raggiunge via host-gateway.
	srv := httptest.NewUnstartedServer(cigateway.New(cigateway.Config{
		Pool: e.pool, Source: e.src, Reader: e.rd, Logs: e.logs, ExternalOnly: true,
		Now: e.now, PollTimeout: 3 * time.Second, PollEvery: 100 * time.Millisecond,
	}))
	l, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	port := l.Addr().(*net.TCPAddr).Port
	host := "127.0.0.1"
	if dockerImage != "" {
		host = "host.docker.internal"
	}
	instance := fmt.Sprintf("http://%s:%d", host, port)

	_, runID := e.queueWF("app", "c", actRunnerWF)
	reg, _, err := ci.IssueRegistrationToken(context.Background(), e.pool, e.now(), nil)
	if err != nil {
		t.Fatal(err)
	}

	work := t.TempDir()
	run := func(timeout time.Duration, args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		var cmd *exec.Cmd
		if dockerImage != "" {
			a := []string{"run", "--rm", "--add-host", "host.docker.internal:host-gateway",
				"-v", bin + ":/usr/local/bin/gitea-runner:ro", "-v", work + ":/work", "-w", "/work",
				"-e", "HOME=/work", dockerImage, "/usr/local/bin/gitea-runner"}
			cmd = exec.CommandContext(ctx, "docker", append(a, args...)...)
		} else {
			cmd = exec.CommandContext(ctx, bin, args...)
			cmd.Dir = work
			cmd.Env = append(os.Environ(), "HOME="+work)
		}
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		return out.String(), err
	}
	line := func(okk bool, name, detail string) {
		t.Helper()
		if okk {
			t.Logf("ok: %s", name)
			return
		}
		t.Errorf("KO: %s: %s", name, detail)
	}

	out, err := run(60*time.Second, "register", "--no-interactive", "--instance", instance,
		"--token", reg, "--name", "act-compat", "--labels", "self-hosted:host")
	line(err == nil, "register con il token monouso", err2s(err)+"\n"+out)
	if err != nil {
		t.FailNow()
	}
	var name string
	var labels []string
	if err := e.pool.QueryRow(context.Background(), `SELECT name, labels FROM core.ci_runners WHERE name = 'act-compat'`).Scan(&name, &labels); err != nil {
		line(false, "runner registrato nel database", err.Error())
		t.FailNow()
	}
	line(true, "runner registrato nel database: "+name+" etichette="+strings.Join(labels, ","), "")
	// Il token monouso non vale due volte.
	if _, err := e.registerWith(reg, "act-compat-2"); err == nil {
		line(false, "token di registrazione monouso", "secondo uso accettato")
	} else {
		line(true, "token di registrazione monouso: secondo uso rifiutato", "")
	}

	out, err = run(120*time.Second, "daemon", "--once")
	t.Logf("--- uscita di gitea-runner daemon --once ---\n%s", out)
	line(err == nil, "daemon --once termina dopo un job", err2s(err))

	var status, concl string
	jobID := e.jobID(runID, "prova")
	if err := e.pool.QueryRow(context.Background(), `SELECT status, COALESCE(conclusion, '') FROM core.ci_jobs WHERE id = $1`, jobID).Scan(&status, &concl); err != nil {
		t.Fatal(err)
	}
	line(status == "completed" && concl == "success", "esito del job: completed/success", status+"/"+concl)

	// GIT-501: gli outputs dichiarati arrivano via UpdateTask.Outputs; il gateway li conferma in
	// SentOutputs, altrimenti act_runner li rimanda e alla chiusura non termina.
	var outs string
	if err := e.pool.QueryRow(context.Background(), `SELECT outputs::text FROM core.ci_jobs WHERE id = $1`, jobID).Scan(&outs); err != nil {
		t.Fatal(err)
	}
	line(strings.Contains(outs, "sha256:compat"), "outputs del job salvati: "+outs, outs)
	line(!strings.Contains(out, "outputs that have not been sent"), "act_runner non ha outputs non confermati", out)

	size, err := e.logs.LogSize(context.Background(), e.pool, jobID)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	if _, err := e.logs.ReadFrom(jobID, 0, size, &sb); err != nil {
		t.Fatal(err)
	}
	logText := sb.String()
	line(strings.Contains(logText, "ACT-RUNNER-PRIMO"), "log: primo passo", logText)
	line(strings.Contains(logText, "ACT-RUNNER-SECONDO job=prova"), "log: secondo passo con GITHUB_JOB", logText)

	var nSteps, nOK int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*), count(*) FILTER (WHERE conclusion = 'success') FROM core.ci_steps WHERE job_id = $1`, jobID).Scan(&nSteps, &nOK); err != nil {
		t.Fatal(err)
	}
	line(nSteps >= 2 && nSteps == nOK, fmt.Sprintf("passi registrati e tutti success (%d/%d)", nOK, nSteps), "")

	var runStatus, runConcl string
	if err := e.pool.QueryRow(context.Background(), `SELECT status, COALESCE(conclusion, '') FROM core.ci_runs WHERE id = $1`, runID).Scan(&runStatus, &runConcl); err != nil {
		t.Fatal(err)
	}
	line(runStatus == "completed" && runConcl == "success", "esito del run: completed/success", runStatus+"/"+runConcl)
}

func err2s(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
