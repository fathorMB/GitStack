//go:build ignore

// check-security-md verifica la presenza e le sezioni di SECURITY.md.
// Si lancia dalla radice del repo:
//
//	go run scripts/check-security-md.go
//
// Le sezioni obbligatorie sono:
// - Supported Versions
// - Reporting a Vulnerability
// - Response Time
// - Public Disclosure
//
// Verifica inoltre che l'indirizzo email di sicurezza sia presente.
package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	b, err := os.ReadFile("SECURITY.md")
	if err != nil {
		fmt.Fprintf(os.Stderr, "check-security-md: SECURITY.md: %v\n", err)
		os.Exit(2)
	}

	content := string(b)
	required := []string{
		"Supported Versions",
		"Reporting a Vulnerability",
		"Response Time",
		"Public Disclosure",
	}

	var missing []string
	for _, s := range required {
		if !strings.Contains(content, "## "+s) {
			missing = append(missing, s)
		}
	}

	// Check that the security email is present.
	if !strings.Contains(content, "moreno.bruschi@live.it") {
		missing = append(missing, "security email (moreno.bruschi@live.it)")
	}

	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "check-security-md: sezioni mancanti: %s\n", strings.Join(missing, ", "))
		os.Exit(1)
	}

	fmt.Printf("check-security-md: SECURITY.md presente con tutte le sezioni obbligatorie\n")
}
