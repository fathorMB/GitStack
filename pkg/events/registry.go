package events

import "fmt"

// Decoder converte il payload grezzo di un evento, nome e versione già
// noti, nel tipo Go concreto usato dal consumer.
type Decoder func(payload []byte) (any, error)

// UnknownSchemaError segnala un evento il cui nome o la cui versione non
// sono registrati lato consumer. Non è un panic: il chiamante decide come
// trattarlo (loggare e scartare, mettere in dead-letter, Term del
// messaggio JetStream) così un producer più recente non fa crashare un
// consumer più vecchio (criterio di GIT-6).
type UnknownSchemaError struct {
	Name    string
	Version int
}

func (e *UnknownSchemaError) Error() string {
	return fmt.Sprintf("events: schema sconosciuto per evento %q versione %d", e.Name, e.Version)
}

// Registry associa nome e versione di un evento al Decoder che ne valida e
// decodifica il payload. Un consumer costruisce il proprio Registry
// registrando solo gli eventi che sa gestire (es. testevent.Register).
type Registry struct {
	decoders map[string]map[int]Decoder
}

// NewRegistry crea un Registry vuoto.
func NewRegistry() *Registry {
	return &Registry{decoders: make(map[string]map[int]Decoder)}
}

// Register registra il Decoder per una coppia nome/versione. Chiamate
// successive con la stessa coppia sovrascrivono la precedente.
func (r *Registry) Register(name string, version int, dec Decoder) {
	if r.decoders[name] == nil {
		r.decoders[name] = make(map[int]Decoder)
	}
	r.decoders[name][version] = dec
}

// Decode valida nome e versione dell'envelope contro il Registry e
// decodifica il payload. Ritorna *UnknownSchemaError, senza panic, se la
// coppia nome/versione non è registrata.
func (r *Registry) Decode(env Envelope) (any, error) {
	versions, ok := r.decoders[env.Name]
	if !ok {
		return nil, &UnknownSchemaError{Name: env.Name, Version: env.Version}
	}
	dec, ok := versions[env.Version]
	if !ok {
		return nil, &UnknownSchemaError{Name: env.Name, Version: env.Version}
	}
	return dec(env.Payload)
}
