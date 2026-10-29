package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/admin/internal/upgrade"
)

// execBinary esegue bin con args, scrive stdout e stderr su out e restituisce l'errore di
// avvio o di uscita. Nei test si sostituisce con App.ExecBinary.
func (a *App) execBinary(ctx context.Context, bin string, args []string, out io.Writer) error {
	if a.ExecBinary != nil {
		return a.ExecBinary(ctx, bin, args, out)
	}
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // binario di destinazione, già scaricato e verificato da upgrade.Run
	cmd.Stdout, cmd.Stderr = out, out
	return cmd.Run()
}

// reexecFunc è upgrade.Options.Reexec: lancia il binario di destinazione con
// `upgrade --reexec-request F --reexec-reply F` (GIT-490), dopo aver verificato con
// `upgrade --reexec-protocol` che lo supporti.
func (a *App) reexecFunc(configPath, dest, keyFile string, timeout time.Duration) func(context.Context, string, *upgrade.ReexecRequest) (*upgrade.ReexecReply, error) {
	return func(ctx context.Context, staged string, req *upgrade.ReexecRequest) (*upgrade.ReexecReply, error) {
		var probe bytes.Buffer
		if err := a.execBinary(ctx, staged, []string{"upgrade", "--reexec-protocol"}, &probe); err != nil || strings.TrimSpace(probe.String()) != upgrade.ReexecProtocol {
			return nil, upgrade.ErrNoReexec
		}
		dir, err := os.MkdirTemp(filepath.Dir(staged), "reexec-")
		if err != nil {
			return nil, err
		}
		defer func() { _ = os.RemoveAll(dir) }()
		reqFile, replyFile := filepath.Join(dir, "request.json"), filepath.Join(dir, "reply.json")
		if err := upgrade.WriteJSON(reqFile, req); err != nil {
			return nil, err
		}
		args := []string{"upgrade", "--config", configPath, "--timeout", timeout.String(),
			"--reexec-request", reqFile, "--reexec-reply", replyFile}
		if dest != "" {
			args = append(args, "--dest", dest)
		}
		if keyFile != "" {
			args = append(args, "--key-file", keyFile)
		}
		if err := a.execBinary(ctx, staged, args, a.Stdout); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(staged), err)
		}
		return upgrade.ReadReexecReply(replyFile)
	}
}

// runReexecChild è `upgrade --reexec-request`: esegue il passo e scrive la risposta.
func (a *App) runReexecChild(ctx context.Context, o *upgrade.Options, reqFile, replyFile string) int {
	if replyFile == "" {
		a.errorf("upgrade: --reexec-request vuole anche --reexec-reply")
		return ExitUsage
	}
	req, err := upgrade.ReadReexecRequest(reqFile)
	if err != nil {
		a.errorf("upgrade: %v", err)
		return ExitUsage
	}
	reply, err := upgrade.RunReexec(ctx, o, req)
	if err != nil {
		a.errorf("upgrade: %v", err)
		return ExitUnexpected
	}
	if err := upgrade.WriteJSON(replyFile, reply); err != nil {
		a.errorf("upgrade: %v", err)
		return ExitUnexpected
	}
	return ExitOK
}
