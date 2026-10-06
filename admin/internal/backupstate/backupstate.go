// Package backupstate gestisce il file di stato dell'ultimo backup: scrittura
// del risultato (successo o errore) e lettura per `gitstack status`.
//
// Il file vive in ConfigDir/backup-state.json, è scritto da root e deve essere
// leggibile da tutti i comandi che lo richiedono.
package backupstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// State è il contenuto del file di stato.
type State struct {
	// Success è vero se l'ultimo backup è andato a buon fine.
	Success bool `json:"success"`
	// Path è il percorso dell'archivio, vuoto in caso di errore.
	Path string `json:"path,omitempty"`
	// Error è il messaggio di errore, vuoto in caso di successo.
	Error string `json:"error,omitempty"`
	// At è l'orario UTC del backup.
	At time.Time `json:"at"`
}

// Filename è il nome del file di stato, rispetto a ConfigDir.
const Filename = "backup-state.json"

// StatePath ritorna il percorso completo del file di stato.
func StatePath(configDir string) string {
	return filepath.Join(configDir, Filename)
}

// Write scrive lo stato. Il file è scritto in atomico (tmp + rename) per
// evitare letture parziali.
func Write(configDir string, s State) error {
	dir := configDir
	if dir == "" {
		dir = "/etc/gitstack"
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, Filename+".tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, Filename))
}

// Read legge lo stato. Se il file non esiste o è illeggibile ritorna un
// valore zero.
func Read(configDir string) State {
	dir := configDir
	if dir == "" {
		dir = "/etc/gitstack"
	}
	data, err := os.ReadFile(filepath.Join(dir, Filename))
	if err != nil {
		return State{}
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}
	}
	return s
}
