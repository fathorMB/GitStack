package templates

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

var wantGitignore = []string{"cpp", "dotnet", "go", "java", "node", "php", "python", "ruby", "rust", "terraform"}

var wantLicenses = []string{"agpl-3.0", "apache-2.0", "bsd-2-clause", "bsd-3-clause", "gpl-3.0", "lgpl-3.0", "mit", "mpl-2.0", "unlicense"}

func TestIDs(t *testing.T) {
	if got := GitignoreIDs(); !reflect.DeepEqual(got, wantGitignore) {
		t.Errorf("GitignoreIDs() = %v, atteso %v", got, wantGitignore)
	}
	if got := LicenseIDs(); !reflect.DeepEqual(got, wantLicenses) {
		t.Errorf("LicenseIDs() = %v, atteso %v", got, wantLicenses)
	}
}

func TestGitignoreNotEmpty(t *testing.T) {
	for _, id := range GitignoreIDs() {
		b, err := Gitignore(id)
		if err != nil || len(bytes.TrimSpace(b)) == 0 {
			t.Errorf("Gitignore(%q): len=%d err=%v", id, len(b), err)
		}
	}
}

func TestLicenseNotEmptyNoPlaceholders(t *testing.T) {
	for _, id := range LicenseIDs() {
		b, err := License(id, 2026, "ACME")
		if err != nil || len(bytes.TrimSpace(b)) == 0 {
			t.Errorf("License(%q): len=%d err=%v", id, len(b), err)
			continue
		}
		if strings.Contains(string(b), "{{") {
			t.Errorf("License(%q): segnaposto non riempito", id)
		}
	}
}

func TestLicensePlaceholdersFilled(t *testing.T) {
	for _, id := range []string{"mit", "bsd-2-clause", "bsd-3-clause"} {
		b, err := License(id, 2026, "ACME")
		if err != nil {
			t.Fatalf("License(%q): %v", id, err)
		}
		if s := string(b); !strings.Contains(s, "2026") || !strings.Contains(s, "ACME") {
			t.Errorf("License(%q) non contiene anno e titolare", id)
		}
	}
}

func TestUnknown(t *testing.T) {
	if _, err := Gitignore("nope"); !errors.Is(err, ErrUnknownTemplate) {
		t.Errorf("Gitignore: err = %v", err)
	}
	if _, err := License("nope", 2026, "x"); !errors.Is(err, ErrUnknownTemplate) {
		t.Errorf("License: err = %v", err)
	}
	if _, err := Gitignore("../licenses/mit"); !errors.Is(err, ErrUnknownTemplate) {
		t.Errorf("Gitignore path: err = %v", err)
	}
}

func TestReadme(t *testing.T) {
	if got := string(Readme("demo", "")); got != "# demo\n" {
		t.Errorf("Readme senza descrizione = %q", got)
	}
	if got := string(Readme("demo", "Una prova")); got != "# demo\n\nUna prova\n" {
		t.Errorf("Readme con descrizione = %q", got)
	}
}
