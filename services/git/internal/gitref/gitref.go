// Package gitref valida e risolve ref, sha e percorsi che arrivano dall'API
// prima che arrivino a git. È il pezzo riusabile dalle altre letture (albero,
// file, branch, tag): nessuna stringa dell'utente diventa un'opzione di git, e
// i ref si passano a git solo dopo essere stati risolti in uno sha.
package gitref

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/fathorMB/GitStack/services/git/internal/gitrun"
)

// Errori del pacchetto (400/404 del contratto).
var (
	ErrInvalidRef  = errors.New("ref non valido")
	ErrRefNotFound = errors.New("ref non trovato")
	ErrInvalidPath = errors.New("percorso non valido")
	ErrInvalidSHA  = errors.New("sha non valido")
)

// Limiti.
const (
	MaxRefLen  = 255
	MaxPathLen = 4096
	MinSHALen  = 7
	MaxSHALen  = 64
)

// ValidateRef controlla un nome di branch, tag o sha: non vuoto, al massimo
// 255 byte, senza `..`, spazi, caratteri di controllo né i caratteri che git
// riserva alle revisioni (`~ ^ : ? * [ \`, `@{`), non iniziale con `-` o `/`.
func ValidateRef(ref string) error {
	if ref == "" || len(ref) > MaxRefLen {
		return fmt.Errorf("%w: lunghezza", ErrInvalidRef)
	}
	if strings.HasPrefix(ref, "-") || strings.HasPrefix(ref, "/") || strings.HasSuffix(ref, "/") ||
		strings.HasSuffix(ref, ".") || strings.HasSuffix(ref, ".lock") || ref == "@" {
		return fmt.Errorf("%w: %q", ErrInvalidRef, ref)
	}
	if strings.Contains(ref, "..") || strings.Contains(ref, "//") || strings.Contains(ref, "@{") {
		return fmt.Errorf("%w: %q", ErrInvalidRef, ref)
	}
	for _, c := range ref {
		if c < 0x20 || c == 0x7f || c == ' ' || strings.ContainsRune("~^:?*[\\", c) {
			return fmt.Errorf("%w: carattere non ammesso", ErrInvalidRef)
		}
	}
	return nil
}

// ValidatePath controlla un percorso relativo alla radice del repo, con `/`
// come separatore. Senza `/` iniziale e senza segmenti `.`, `..` o vuoti;
// un `/` finale (cartella) è tollerato e tolto. Vuoto vale la radice se
// required è false. Torna il percorso pulito.
func ValidatePath(p string, required bool) (string, error) {
	if p == "" {
		if required {
			return "", fmt.Errorf("%w: obbligatorio", ErrInvalidPath)
		}
		return "", nil
	}
	if len(p) > MaxPathLen || strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("%w: %q", ErrInvalidPath, p)
	}
	p = strings.TrimSuffix(p, "/")
	if p == "" {
		return "", fmt.Errorf("%w: radice con /", ErrInvalidPath)
	}
	for _, c := range p {
		if c < 0x20 || c == 0x7f || c == '\\' {
			return "", fmt.Errorf("%w: carattere non ammesso", ErrInvalidPath)
		}
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf("%w: segmento %q", ErrInvalidPath, seg)
		}
	}
	return p, nil
}

// IsHexSHA dice se s è uno sha completo o un prefisso di almeno 7 cifre
// esadecimali minuscole.
func IsHexSHA(s string) bool {
	if len(s) < MinSHALen || len(s) > MaxSHALen {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// ResolveCommit risolve uno sha (completo o prefisso) in uno sha completo di
// commit. ErrInvalidSHA se non è esadecimale, ErrRefNotFound se non esiste (o
// il prefisso è ambiguo, o non è un commit).
func ResolveCommit(ctx context.Context, r *gitrun.Runner, gitDir, sha string) (string, error) {
	if !IsHexSHA(sha) {
		return "", ErrInvalidSHA
	}
	return revParse(ctx, r, gitDir, sha)
}

// Resolve risolve un branch, un tag o uno sha nello sha completo del commit.
// Se un nome è sia branch sia tag vince il branch; se è anche esadecimale
// vincono branch e tag sul prefisso di sha.
func Resolve(ctx context.Context, r *gitrun.Runner, gitDir, ref string) (string, error) {
	if err := ValidateRef(ref); err != nil {
		return "", err
	}
	for _, prefix := range []string{"refs/heads/", "refs/tags/"} {
		sha, err := revParse(ctx, r, gitDir, prefix+ref)
		if err == nil {
			return sha, nil
		}
		if !errors.Is(err, ErrRefNotFound) {
			return "", err
		}
	}
	if IsHexSHA(ref) {
		return revParse(ctx, r, gitDir, ref)
	}
	return "", ErrRefNotFound
}

// revParse risolve un'espressione che chi chiama ha già validato (un nome
// completo `refs/...` o una stringa esadecimale) in un commit.
func revParse(ctx context.Context, r *gitrun.Runner, gitDir, expr string) (string, error) {
	out, err := r.Output(ctx, gitDir, nil, "rev-parse", "--verify", "--quiet", "--end-of-options", expr+"^{commit}")
	if err != nil {
		var ge *gitrun.Error
		if errors.As(err, &ge) {
			if ge.ExitCode == 1 || (ge.ExitCode == 128 && strings.Contains(ge.Stderr, "ambiguous")) {
				return "", ErrRefNotFound
			}
		}
		return "", err
	}
	sha := strings.TrimSpace(string(out))
	if !IsHexSHA(sha) || len(sha) < 40 {
		return "", fmt.Errorf("gitref: risposta inattesa di rev-parse: %q", sha)
	}
	return sha, nil
}
