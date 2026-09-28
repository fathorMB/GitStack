// Package testevent definisce l'evento di prova del bus NATS JetStream di
// GitStack: nome, versione 1 e payload. Serve a due scopi.
//
//  1. Verificare qui, in pkg/events, l'intera catena publish-con-conferma /
//     consumer-durevole / validazione-di-schema contro un server NATS
//     JetStream reale (criteri di GIT-6).
//  2. Essere l'evento che il servizio core pubblica alla creazione della
//     risorsa di prova (GIT-5), tramite pkg/events.
//
// Nome, versione e payload coincidono con quelli già usati dal placeholder
// services/core/internal/events (events.TestResourceCreated*) introdotto in
// GIT-5 in attesa di questa libreria: l'aggancio in GIT-5 diventa un
// adapter locale, non un nuovo schema.
package testevent

import (
	"encoding/json"
	"fmt"

	"github.com/fathorMB/GitStack/pkg/events"
)

// Name è il nome dell'evento, ed è anche il subject NATS su cui viene
// pubblicato (convenzioni: docs/events.md). Dominio "core" (primo
// segmento): lo stream che lo raccoglie è CORE.
const Name = "core.resource.test.created"

// Version è la versione corrente dello schema del payload.
const Version = 1

// Payload è lo schema v1 dell'evento di prova.
type Payload struct {
	ResourceID string `json:"resourceId"`
	Type       string `json:"type"`
	Name       string `json:"name"`
}

// Decode valida e decodifica il payload grezzo secondo lo schema v1. È
// l'events.Decoder registrato per (Name, Version).
func Decode(raw []byte) (any, error) {
	var p Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("testevent: decode payload v%d: %w", Version, err)
	}
	return p, nil
}

// Register registra il Decoder dell'evento di prova su reg: da chiamare
// all'avvio di ogni consumer che deve capirlo.
func Register(reg *events.Registry) {
	reg.Register(Name, Version, Decode)
}
