package events

import (
	"fmt"
	"strings"
)

// Domain estrae il dominio di un evento (primo segmento del nome, che è
// anche il subject NATS): "core.resource.test.created" -> "core",
// "git.push" -> "git". Convenzioni complete: docs/events.md.
func Domain(eventName string) string {
	if i := strings.IndexByte(eventName, '.'); i >= 0 {
		return eventName[:i]
	}
	return eventName
}

// StreamName deriva il nome dello stream JetStream dal dominio di un
// evento: SCREAMING_SNAKE_CASE del dominio (es. "core" -> "CORE"). Vedi
// docs/events.md per la convenzione completa (uno stream per dominio,
// subject "<dominio>.>").
func StreamName(domain string) (string, error) {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return "", fmt.Errorf("events: dominio vuoto")
	}
	return strings.ToUpper(domain), nil
}
