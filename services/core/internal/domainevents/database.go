package domainevents

import "encoding/json"

// Eventi dei database gestiti (v3-d, ADR 0003, DB20). Schema fissato in docs/events.md: nessun
// payload contiene segreti (password dell'utente proprietario o dei collegamenti, contenuti dei dump).
const (
	DatabaseCreated         = "database.created"
	DatabaseUpdated         = "database.updated"
	DatabaseDeleted         = "database.deleted"
	DatabaseLinked          = "database.linked"
	DatabaseUnlinked        = "database.unlinked"
	DatabaseLanEnabled      = "database.lan_enabled"
	DatabaseLanDisabled     = "database.lan_disabled"
	DatabaseBackupCompleted = "database.backup_completed"
	DatabaseBackupFailed    = "database.backup_failed"
	DatabaseRestored        = "database.restored"

	// DatabaseQuotaWarning: avviso di disco dal limite morbido (80% e 100% di pg_database_size, DB3).
	DatabaseQuotaWarning = "database.quota_warning"
)

// DomainDatabase e' il primo segmento dei nomi sopra (stream DATABASE, subject database.>).
const DomainDatabase = "database"

// Database e' l'istantanea minima della risorsa nei payload (`database` in docs/events.md).
// OwnerID e OwnerType (non segreti) servono ai consumer fuori dal database di core — il motore dei
// webhook li usa per trovare i webhook dell'organizzazione anche quando la riga del database e'
// gia' stata eliminata (database.deleted) e i repo non bastano (un'org puo' non averne, GIT-568).
type Database struct {
	ID        string `json:"id"`
	Owner     string `json:"owner"`
	OwnerID   string `json:"ownerId"`
	OwnerType string `json:"ownerType"`
	Name      string `json:"name"`
	PgMajor   int    `json:"pgMajor"`
}

// DatabasePayload e' lo schema interno di database.*. I campi opzionali dipendono dall'evento:
// created porta sizeBytes/cpuMillis/memoryBytes, updated `changes` coi valori precedenti, deleted
// `deletedAt`, quota_warning `diskState`/`usageBytes`.
type DatabasePayload struct {
	Database    Database        `json:"database"`
	Actor       *User           `json:"actor"`
	SizeBytes   *int64          `json:"sizeBytes,omitempty"`
	CPUMillis   *int            `json:"cpuMillis,omitempty"`
	MemoryBytes *int64          `json:"memoryBytes,omitempty"`
	Changes     json.RawMessage `json:"changes,omitempty"`
	DeletedAt   string          `json:"deletedAt,omitempty"`
	Link        json.RawMessage `json:"link,omitempty"`
	DiskState   string          `json:"diskState,omitempty"`
	UsageBytes  *int64          `json:"usageBytes,omitempty"`
	// Port: porta TCP della LAN, solo in lan_enabled (assente con la LAN spenta, DB16).
	Port *int `json:"port,omitempty"`
}
