package databases

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Executor esegue un comando nel container del pod del database (pods/exec, ADR punto 5).
type Executor interface {
	Exec(ctx context.Context, namespace, pod, container string, cmd []string, stdin io.Reader) (stdout string, err error)
}

// SPDYExecutor e' l'Executor di produzione e dei test su k3d.
type SPDYExecutor struct {
	Config *rest.Config
	Client kubernetes.Interface
}

// Exec implementa Executor.
func (e *SPDYExecutor) Exec(ctx context.Context, namespace, pod, container string, cmd []string, stdin io.Reader) (string, error) {
	req := e.Client.CoreV1().RESTClient().Post().Resource("pods").Namespace(namespace).Name(pod).SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{Container: container, Command: cmd, Stdin: stdin != nil, Stdout: true, Stderr: true}, scheme.ParameterCodec)
	ex, err := remotecommand.NewSPDYExecutor(e.Config, http.MethodPost, req.URL())
	if err != nil {
		return "", err
	}
	var out, errb bytes.Buffer
	if err := ex.StreamWithContext(ctx, remotecommand.StreamOptions{Stdin: stdin, Stdout: &out, Stderr: &errb}); err != nil {
		return out.String(), fmt.Errorf("%w: %s", err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// SQLRunner esegue SQL come gitstack_admin dentro il database. In produzione e' psql nel pod sul socket
// locale (PsqlRunner); i test d'integrazione ne danno uno su un Postgres vero (container).
type SQLRunner interface {
	// Exec esegue uno o piu' statement nel database `database`, senza risultato.
	Exec(ctx context.Context, d store.ManagedDB, database, sql string) error
	// Query esegue UNO statement e ritorna la prima colonna di ogni riga.
	Query(ctx context.Context, d store.ManagedDB, database, sql string) ([]string, error)
}

// PsqlRunner implementa SQLRunner con `psql` nel pod. L'SQL (che contiene le password) passa da stdin,
// mai dalla riga di comando, e gli errori sono ripuliti dai segreti noti.
type PsqlRunner struct {
	Pod Executor
	// Secrets sono i valori da togliere dagli errori (password appena generate).
	Redact func() []string
}

func (p *PsqlRunner) run(ctx context.Context, d store.ManagedDB, database, sql string, tuples bool) (string, error) {
	cmd := []string{"psql", "-X", "-q", "-v", "ON_ERROR_STOP=1", "-h", "/var/run/postgresql", "-U", AdminRole, "-d", database, "-f", "-"}
	if tuples {
		cmd = append(cmd, "-t", "-A")
	}
	out, err := p.Pod.Exec(ctx, Namespace(d.ID), PodName, WorkloadName, cmd, strings.NewReader(sql))
	if err != nil {
		return "", errors.New(p.scrub(err.Error()))
	}
	return out, nil
}

func (p *PsqlRunner) scrub(s string) string {
	if p.Redact != nil {
		for _, v := range p.Redact() {
			if v != "" {
				s = strings.ReplaceAll(s, v, "***")
			}
		}
	}
	return s
}

// Exec implementa SQLRunner.
func (p *PsqlRunner) Exec(ctx context.Context, d store.ManagedDB, database, sql string) error {
	_, err := p.run(ctx, d, database, sql, false)
	return err
}

// Query implementa SQLRunner.
func (p *PsqlRunner) Query(ctx context.Context, d store.ManagedDB, database, sql string) ([]string, error) {
	out, err := p.run(ctx, d, database, sql, true)
	if err != nil {
		return nil, err
	}
	var rows []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimRight(l, "\r"); l != "" {
			rows = append(rows, l)
		}
	}
	return rows, nil
}

// ImageVerifier verifica la firma cosign del digest PRIMA dello StatefulSet (DB5, H10). Un errore blocca
// la creazione: il database va in `failed` con il motivo.
type ImageVerifier interface {
	Verify(ctx context.Context, imageRef string) error
}

// CosignCLI verifica con `cosign verify --key <chiave pubblica>` del registry dell'istanza.
type CosignCLI struct {
	Binary  string
	KeyFile string
	Timeout time.Duration
}

// Verify implementa ImageVerifier.
func (c CosignCLI) Verify(ctx context.Context, imageRef string) error {
	bin := c.Binary
	if bin == "" {
		bin = "cosign"
	}
	if c.KeyFile == "" {
		return errors.New("verifica cosign non configurata: manca la chiave pubblica (GITSTACK_CORE_DATABASES_COSIGN_KEY_FILE)")
	}
	t := c.Timeout
	if t <= 0 {
		t = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, t)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "verify", "--key", c.KeyFile, imageRef).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return fmt.Errorf("firma cosign dell'immagine non verificata: %v: %s", err, msg)
	}
	return nil
}
