package events

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Envelope è la busta comune di ogni evento pubblicato sul bus: nome,
// versione dello schema, id univoco, timestamp di creazione (UTC) e
// payload applicativo in JSON grezzo. Il nome dell'evento è anche il
// subject NATS su cui viaggia (convenzioni: docs/events.md).
type Envelope struct {
	Name    string          `json:"name"`
	Version int             `json:"version"`
	ID      string          `json:"id"`
	Time    time.Time       `json:"time"`
	Payload json.RawMessage `json:"payload"`
}

// NewEnvelope costruisce l'envelope di un evento serializzando payload in
// JSON. ID (uuid v4) e Time (UTC, ora corrente) sono generati qui: il
// chiamante non deve preoccuparsene.
func NewEnvelope(name string, version int, payload any) (Envelope, error) {
	if name == "" {
		return Envelope{}, fmt.Errorf("events: nome evento vuoto")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("events: marshal payload di %q v%d: %w", name, version, err)
	}
	return Envelope{
		Name:    name,
		Version: version,
		ID:      uuid.NewString(),
		Time:    time.Now().UTC(),
		Payload: raw,
	}, nil
}

// Marshal serializza l'envelope in JSON, così come viaggia sul subject.
func (e Envelope) Marshal() ([]byte, error) {
	data, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("events: marshal envelope %q v%d: %w", e.Name, e.Version, err)
	}
	return data, nil
}

// UnmarshalEnvelope decodifica la busta di un messaggio ricevuto dal bus.
// Non valida ancora nome/versione contro uno schema noto: quello è compito
// di Registry.Decode, lato consumer.
func UnmarshalEnvelope(data []byte) (Envelope, error) {
	var e Envelope
	if err := json.Unmarshal(data, &e); err != nil {
		return Envelope{}, fmt.Errorf("events: decode envelope: %w", err)
	}
	return e, nil
}
