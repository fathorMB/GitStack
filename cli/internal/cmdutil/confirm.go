package cmdutil

import (
	"bufio"
	"fmt"
	"strings"
)

// ConfirmOrYes è la conferma G8 per le operazioni distruttive.
//
//   - yes (flag --yes): conferma senza chiedere.
//   - senza TTY in ingresso e senza yes: UsageError (exit 2), senza leggere nulla.
//   - con TTY: scrive prompt su stderr e legge una riga. Se expected non è
//     vuoto bisogna riscriverlo identico (per esempio owner/repo); se è vuoto
//     bastano "y" o "yes". Altrimenti ErrCancelled (exit 1).
func ConfirmOrYes(io *IOStreams, yes bool, prompt, expected string) error {
	if yes {
		return nil
	}
	if !io.InTTY {
		return UsageErrorf("conferma richiesta: rilancia con --yes per procedere senza terminale")
	}
	_, _ = fmt.Fprint(io.ErrOut, prompt)
	if !strings.HasSuffix(prompt, " ") && !strings.HasSuffix(prompt, "\n") {
		_, _ = fmt.Fprint(io.ErrOut, " ")
	}
	line, err := bufio.NewReader(io.In).ReadString('\n')
	if err != nil && line == "" {
		return ErrCancelled
	}
	answer := strings.TrimSpace(line)
	if expected != "" {
		if answer != expected {
			return fmt.Errorf("%w: il testo digitato non coincide con %q", ErrCancelled, expected)
		}
		return nil
	}
	if a := strings.ToLower(answer); a != "y" && a != "yes" && a != "s" && a != "si" && a != "sì" {
		return ErrCancelled
	}
	return nil
}
