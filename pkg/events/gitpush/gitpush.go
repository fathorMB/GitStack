// Package gitpush definisce l'evento git.push: nome, versione, payload e
// decoder (docs/events.md). Lo pubblica il servizio git dopo ogni push
// accettato, via HTTPS o SSH.
//
// Consumatori previsti (regole M-06): core chiude le issues con `fixes #n`
// solo quando il commit entra nel branch principale (C2) e solo se chi ha
// fatto il push ha write sul repo della issue (C1); il webhook push (C6)
// è ispirato a quello di GitHub.
package gitpush

import (
	"encoding/json"
	"fmt"

	"github.com/fathorMB/GitStack/pkg/events"
)

// Name è il nome dell'evento e il subject NATS; dominio "git", stream GIT.
const Name = "git.push"

// Version è la versione corrente dello schema del payload.
const Version = 1

// MaxCommits è il massimo di commit elencati per ref. Oltre, l'elenco è
// troncato (i più recenti per primi, come `git log`) e CommitsTruncated è
// true: il resto si ricostruisce da Before..After (docs/events.md).
const MaxCommits = 100

// ZeroSHA è lo sha di Before per un ref creato e di After per un ref eliminato.
const ZeroSHA = "0000000000000000000000000000000000000000"

// Tipi di utente di chi ha fatto il push.
const (
	PusherHuman = "human"
	PusherAgent = "agent"
)

// Payload è lo schema v1 dell'evento git.push.
type Payload struct {
	Repo   Repo      `json:"repo"`
	Pusher Pusher    `json:"pusher"`
	Refs   []RefPush `json:"refs"`
}

// Repo identifica il repo e il suo branch principale al momento del push (R4).
type Repo struct {
	ID            string `json:"id"`
	FullName      string `json:"fullName"` // owner/repo
	DefaultBranch string `json:"defaultBranch"`
}

// Pusher è l'utente autenticato che ha fatto il push, non l'autore dei commit.
type Pusher struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Type     string `json:"type"` // human | agent
}

// RefPush è un ref (branch o tag) aggiornato dal push.
type RefPush struct {
	// Ref è il nome completo: refs/heads/main, refs/tags/v1.
	Ref string `json:"ref"`
	// Before è lo sha prima del push (ZeroSHA se il ref è stato creato).
	Before string `json:"before"`
	// After è lo sha dopo il push (ZeroSHA se il ref è stato eliminato).
	After string `json:"after"`
	// Forced: il vecchio sha non è antenato del nuovo (solo per gli
	// aggiornamenti; false per creazione ed eliminazione).
	Forced bool `json:"forced"`
	// IsDefaultBranch: il ref è il branch principale del repo.
	IsDefaultBranch bool `json:"isDefaultBranch"`
	// Commits sono i commit nuovi raggiungibili da After, i più recenti per
	// primi, fino a MaxCommits; vuoto per un ref eliminato.
	Commits []Commit `json:"commits"`
	// CommitsTruncated: i commit nuovi sono più di MaxCommits.
	CommitsTruncated bool `json:"commitsTruncated"`
}

// Commit è un commit nuovo.
type Commit struct {
	SHA       string `json:"sha"`
	Author    Person `json:"author"`
	Committer Person `json:"committer"`
	// Message è il messaggio completo.
	Message string `json:"message"`
}

// Person è autore o committer di un commit; Date è RFC 3339.
type Person struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Date  string `json:"date"`
}

// Decode valida e decodifica il payload v1: è l'events.Decoder registrato.
func Decode(raw []byte) (any, error) {
	var p Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("gitpush: decode payload v%d: %w", Version, err)
	}
	if p.Repo.ID == "" || p.Pusher.ID == "" {
		return nil, fmt.Errorf("gitpush: payload v%d senza repo.id o pusher.id", Version)
	}
	return p, nil
}

// Register registra il Decoder su reg: da chiamare nei consumer (core).
func Register(reg *events.Registry) {
	reg.Register(Name, Version, Decode)
}
