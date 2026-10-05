// Package templates contiene i modelli di contenuto iniziale di un repository
// (R5): .gitignore, licenze e README. I testi sono incorporati con embed.
package templates

import (
	"embed"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

//go:embed gitignore/*.gitignore licenses/*.txt
var files embed.FS

// ErrUnknownTemplate è restituito (eventualmente avvolto) per un id sconosciuto.
var ErrUnknownTemplate = errors.New("templates: modello sconosciuto")

var gitignoreIDs = []string{
	"cpp", "dotnet", "go", "java", "node", "php", "python", "ruby", "rust", "terraform",
}

var licenseIDs = []string{
	"agpl-3.0", "apache-2.0", "bsd-2-clause", "bsd-3-clause", "gpl-3.0",
	"lgpl-3.0", "mit", "mpl-2.0", "unlicense",
}

// GitignoreIDs restituisce gli id dei modelli .gitignore, ordinati.
func GitignoreIDs() []string { return sortedCopy(gitignoreIDs) }

// LicenseIDs restituisce gli id delle licenze, ordinati.
func LicenseIDs() []string { return sortedCopy(licenseIDs) }

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func contains(list []string, id string) bool {
	for _, v := range list {
		if v == id {
			return true
		}
	}
	return false
}

// Gitignore restituisce il testo del modello .gitignore con l'id dato.
func Gitignore(id string) ([]byte, error) {
	if !contains(gitignoreIDs, id) {
		return nil, fmt.Errorf("%w: .gitignore %q", ErrUnknownTemplate, id)
	}
	return files.ReadFile("gitignore/" + id + ".gitignore")
}

// License restituisce il testo della licenza con l'id dato, sostituendo i
// segnaposto {{year}} e {{holder}} dove la licenza li prevede.
func License(id string, year int, holder string) ([]byte, error) {
	if !contains(licenseIDs, id) {
		return nil, fmt.Errorf("%w: licenza %q", ErrUnknownTemplate, id)
	}
	b, err := files.ReadFile("licenses/" + id + ".txt")
	if err != nil {
		return nil, err
	}
	r := strings.NewReplacer("{{year}}", strconv.Itoa(year), "{{holder}}", holder)
	return []byte(r.Replace(string(b))), nil
}

// Readme restituisce un README con il titolo "# <name>" e, se non vuota, la
// descrizione dopo una riga vuota.
func Readme(name, description string) []byte {
	s := "# " + name + "\n"
	if description != "" {
		s += "\n" + description + "\n"
	}
	return []byte(s)
}
