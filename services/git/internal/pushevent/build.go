// Package pushevent costruisce e pubblica l'evento git.push (M-03/K):
// confronta i ref del repo prima e dopo un receive-pack riuscito, ricava i
// commit nuovi di ogni ref e pubblica il payload di pkg/events/gitpush
// fuori dal percorso della risposta al client. Se NATS non risponde il push
// resta accettato: la pubblicazione si ritenta e, se non riesce, l'errore
// finisce nel log (docs/events.md).
package pushevent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/fathorMB/GitStack/pkg/events/gitpush"
	"github.com/fathorMB/GitStack/services/git/internal/gitrun"
)

// Snapshot è l'elenco dei ref di un repo: nome completo → sha. Contiene
// solo branch e tag (refs/heads/, refs/tags/).
type Snapshot map[string]string

// Take legge i ref del repo bare in dir.
func Take(ctx context.Context, r *gitrun.Runner, dir string) (Snapshot, error) {
	out, err := r.Output(ctx, dir, nil, "for-each-ref", "--format=%(objectname) %(refname)", "refs/heads", "refs/tags")
	if err != nil {
		return nil, err
	}
	snap := Snapshot{}
	for _, line := range strings.Split(string(out), "\n") {
		sha, ref, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || sha == "" || ref == "" {
			continue
		}
		snap[ref] = sha
	}
	return snap, nil
}

// Build confronta due snapshot e ritorna un RefPush per ogni ref creato,
// aggiornato o eliminato, ordinati per nome. defaultBranch è il nome corto
// del branch principale.
func Build(ctx context.Context, r *gitrun.Runner, dir string, before, after Snapshot, defaultBranch string) ([]gitpush.RefPush, error) {
	names := map[string]struct{}{}
	for n := range before {
		names[n] = struct{}{}
	}
	for n := range after {
		names[n] = struct{}{}
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)

	var errs []error
	var out []gitpush.RefPush
	for _, name := range sorted {
		b, hadB := before[name]
		a, hadA := after[name]
		if hadB && hadA && a == b {
			continue
		}
		if !hadB {
			b = gitpush.ZeroSHA
		}
		if !hadA {
			a = gitpush.ZeroSHA
		}
		rp := gitpush.RefPush{
			Ref: name, Before: b, After: a,
			IsDefaultBranch: defaultBranch != "" && name == "refs/heads/"+defaultBranch,
			Commits:         []gitpush.Commit{},
		}
		if hadB && hadA {
			forced, err := isForced(ctx, r, dir, b, a)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", name, err))
			}
			rp.Forced = forced
		}
		if hadA {
			commits, truncated, err := newCommits(ctx, r, dir, a, b, hadB, before)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: commit: %w", name, err))
			}
			rp.Commits, rp.CommitsTruncated = commits, truncated
		}
		out = append(out, rp)
	}
	return out, errors.Join(errs...)
}

// isForced: il vecchio sha non è antenato del nuovo. Se git non sa
// rispondere (oggetto non commit, errore) ritorna true, la scelta
// prudente, insieme all'errore.
func isForced(ctx context.Context, r *gitrun.Runner, dir, oldSHA, newSHA string) (bool, error) {
	_, err := r.Output(ctx, dir, nil, "merge-base", "--is-ancestor", oldSHA, newSHA)
	if err == nil {
		return false, nil
	}
	var ge *gitrun.Error
	if errors.As(err, &ge) && ge.ExitCode == 1 {
		return true, nil
	}
	return true, err
}

const (
	fieldSep  = "\x1f"
	recordSep = "\x1e"
	logFormat = "%H" + fieldSep + "%an" + fieldSep + "%ae" + fieldSep + "%aI" + fieldSep +
		"%cn" + fieldSep + "%ce" + fieldSep + "%cI" + fieldSep + "%B" + recordSep
)

// newCommits elenca i commit nuovi di un ref: per un aggiornamento quelli
// raggiungibili da newSHA e non da oldSHA (l'intervallo before..after); per
// una creazione quelli non raggiungibili da nessun altro ref del repo
// prima del push. Al massimo gitpush.MaxCommits, i più recenti per primi.
func newCommits(ctx context.Context, r *gitrun.Runner, dir, newSHA, oldSHA string, hadOld bool, before Snapshot) ([]gitpush.Commit, bool, error) {
	var in bytes.Buffer
	in.WriteString(newSHA + "\n")
	if hadOld {
		in.WriteString("^" + oldSHA + "\n")
	} else {
		seen := map[string]bool{}
		for _, sha := range before {
			if !seen[sha] {
				seen[sha] = true
				in.WriteString("^" + sha + "\n")
			}
		}
	}
	out, err := r.Output(ctx, dir, &in, "log", "--stdin", "--format="+logFormat, fmt.Sprintf("--max-count=%d", gitpush.MaxCommits+1))
	if err != nil {
		return []gitpush.Commit{}, false, err
	}
	commits := []gitpush.Commit{}
	for _, rec := range strings.Split(string(out), recordSep) {
		rec = strings.TrimPrefix(rec, "\n")
		if rec == "" {
			continue
		}
		f := strings.SplitN(rec, fieldSep, 8)
		if len(f) != 8 {
			return []gitpush.Commit{}, false, fmt.Errorf("riga di log non valida: %q", rec)
		}
		commits = append(commits, gitpush.Commit{
			SHA:       f[0],
			Author:    gitpush.Person{Name: f[1], Email: f[2], Date: f[3]},
			Committer: gitpush.Person{Name: f[4], Email: f[5], Date: f[6]},
			Message:   f[7],
		})
	}
	if len(commits) > gitpush.MaxCommits {
		return commits[:gitpush.MaxCommits], true, nil
	}
	return commits, false, nil
}
