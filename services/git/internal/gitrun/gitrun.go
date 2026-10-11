// Package gitrun lancia il binario `git` ufficiale per le letture: ambiente
// ripulito dalle variabili GIT_*, nessuna configurazione di sistema o utente,
// niente prompt, un timeout su ogni comando e un tetto sull'output tenuto in
// memoria. Gli argomenti li costruisce chi chiama dopo averli validati
// (pacchetto gitref): qui non si passa mai una stringa dell'utente come
// opzione.
package gitrun

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Valori di default.
const (
	// DefaultTimeout è il tempo massimo di un comando che torna un risultato.
	DefaultTimeout = 30 * time.Second
	// DefaultStreamTimeout è il tempo massimo di un comando in streaming
	// (download di diff e patch).
	DefaultStreamTimeout = 5 * time.Minute
	// DefaultMaxOutput è il tetto dell'output tenuto in memoria.
	DefaultMaxOutput = 32 << 20
)

// ErrOutputTooLarge: l'output del comando supera il tetto.
var ErrOutputTooLarge = errors.New("gitrun: output troppo grande")

// Error è un comando git finito con errore.
type Error struct {
	Cmd      string
	ExitCode int // -1 se il processo non è partito o è stato interrotto
	Stderr   string
	Err      error
}

func (e *Error) Error() string {
	return fmt.Sprintf("git %s: %v: %s", e.Cmd, e.Err, e.Stderr)
}

func (e *Error) Unwrap() error { return e.Err }

// Runner esegue git.
type Runner struct {
	// Bin è il percorso del binario git.
	Bin string
	// Timeout per Output; zero = DefaultTimeout.
	Timeout time.Duration
	// StreamTimeout per Stream; zero = DefaultStreamTimeout.
	StreamTimeout time.Duration
	// MaxOutput per Output; zero = DefaultMaxOutput.
	MaxOutput int
}

// New cerca git nel PATH.
func New() (*Runner, error) {
	bin, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("gitrun: binario git non trovato: %w", err)
	}
	return &Runner{Bin: bin}, nil
}

func (r *Runner) timeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return DefaultTimeout
}

func (r *Runner) streamTimeout() time.Duration {
	if r.StreamTimeout > 0 {
		return r.StreamTimeout
	}
	return DefaultStreamTimeout
}

func (r *Runner) maxOutput() int {
	if r.MaxOutput > 0 {
		return r.MaxOutput
	}
	return DefaultMaxOutput
}

// Env è l'ambiente dei processi git.
func Env() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(strings.ToUpper(kv), "GIT_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
}

func (r *Runner) command(ctx context.Context, gitDir string, args []string) *exec.Cmd {
	full := append([]string{
		"--git-dir=" + gitDir,
		"--literal-pathspecs",
		"-c", "core.quotepath=false",
		"-c", "diff.renames=true",
	}, args...)
	cmd := exec.CommandContext(ctx, r.Bin, full...)
	cmd.Env = Env()
	// Un figlio di git (o un wrapper) puo' tenere aperte le pipe dopo la kill
	// del contesto: senza WaitDelay Wait aspetterebbe oltre il timeout.
	cmd.WaitDelay = 3 * time.Second
	return cmd
}

// limitedBuffer scarta il superamento del tetto segnalandolo.
type limitedBuffer struct {
	buf      bytes.Buffer
	max      int
	exceeded bool
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if l.buf.Len()+len(p) > l.max {
		l.exceeded = true
		return 0, ErrOutputTooLarge
	}
	return l.buf.Write(p)
}

// Output esegue `git --git-dir=gitDir args...` con il timeout e torna lo
// stdout. stdin può essere nil.
func (r *Runner) Output(ctx context.Context, gitDir string, stdin io.Reader, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout())
	defer cancel()
	cmd := r.command(ctx, gitDir, args)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	out := &limitedBuffer{max: r.maxOutput()}
	var errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = out, &errb
	if err := cmd.Run(); err != nil {
		if out.exceeded {
			return nil, ErrOutputTooLarge
		}
		return nil, wrap(ctx, args, err, errb.String())
	}
	return out.buf.Bytes(), nil
}

// Stream esegue il comando scrivendo lo stdout su w man mano che arriva,
// con StreamTimeout. Un errore di w interrompe il comando.
func (r *Runner) Stream(ctx context.Context, gitDir string, w io.Writer, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, r.streamTimeout())
	defer cancel()
	cmd := r.command(ctx, gitDir, args)
	var errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = w, &errb
	if err := cmd.Run(); err != nil {
		return wrap(ctx, args, err, errb.String())
	}
	return nil
}

func wrap(ctx context.Context, args []string, err error, stderr string) error {
	e := &Error{Cmd: firstArg(args), ExitCode: -1, Stderr: strings.TrimSpace(stderr), Err: err}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		e.ExitCode = ee.ExitCode()
	}
	if ctx.Err() != nil {
		e.Err = ctx.Err()
		e.ExitCode = -1
	}
	return e
}

func firstArg(a []string) string {
	if len(a) == 0 {
		return ""
	}
	return a[0]
}

// Result è l'esito di un comando lanciato con RunEnv: stdout e stderr
// completi (entro il tetto) e il codice di uscita.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// RunEnv esegue `git --git-dir=gitDir pre... args...` con l'ambiente ripulito
// (Env) più extraEnv, un timeout esplicito e senza mai dare un errore per un
// codice di uscita diverso da zero: chi chiama legge Result (un push
// rifiutato esce con 1 ma il suo stdout porcelain è l'esito). L'errore è non
// nil solo se il processo non è partito, è scaduto il tempo o l'output supera
// il tetto. Serve ai comandi che parlano con un server remoto: le credenziali
// passano da extraEnv, mai da argv.
func (r *Runner) RunEnv(ctx context.Context, gitDir string, timeout time.Duration, extraEnv []string, args ...string) (Result, error) {
	if timeout <= 0 {
		timeout = r.timeout()
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := r.command(ctx, gitDir, args)
	cmd.Env = append(cmd.Env, extraEnv...)
	out := &limitedBuffer{max: r.maxOutput()}
	errb := &limitedBuffer{max: r.maxOutput()}
	cmd.Stdout, cmd.Stderr = out, errb
	// Un figlio (git-remote-https) può tenere aperte le pipe dopo la kill: senza
	// WaitDelay Run aspetterebbe oltre il timeout.
	cmd.WaitDelay = 3 * time.Second
	err := cmd.Run()
	res := Result{Stdout: out.buf.Bytes(), Stderr: errb.buf.Bytes()}
	if err == nil {
		return res, nil
	}
	if ctx.Err() != nil {
		return res, &Error{Cmd: firstArg(args), ExitCode: -1, Err: ctx.Err()}
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && !out.exceeded && !errb.exceeded {
		res.ExitCode = ee.ExitCode()
		return res, nil
	}
	if out.exceeded || errb.exceeded {
		return res, ErrOutputTooLarge
	}
	return res, &Error{Cmd: firstArg(args), ExitCode: -1, Err: err}
}
