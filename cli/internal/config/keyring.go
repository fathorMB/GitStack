package config

import "github.com/zalando/go-keyring"

// KeyringService è il nome del servizio nel portachiavi di sistema; la voce
// per ogni istanza ha come utente il nome dell'host.
const KeyringService = "gs"

// Sorgente di un token conservato nel portachiavi, per `gs auth status`.
const SourceKeyring = "keyring"

// KeyringTokens conserva i token nel portachiavi di sistema quando c'è e,
// se non è disponibile (niente Secret Service su un server, sessione senza
// D-Bus, ...), nel file hosts/<host>.yaml solo-utente (FileTokens).
//
// Un token già nel file (di un login fatto senza portachiavi) si legge
// comunque; al primo SetToken riuscito nel portachiavi la copia nel file si
// toglie.
type KeyringTokens struct {
	Cfg *Config
}

func (k KeyringTokens) file() FileTokens { return FileTokens(k) }

// Token cerca prima nel portachiavi, poi nel file.
func (k KeyringTokens) Token(host string) (string, string, error) {
	tok, err := keyring.Get(KeyringService, host)
	if err == nil && tok != "" {
		return tok, SourceKeyring, nil
	}
	return k.file().Token(host)
}

// SetToken prova il portachiavi; se fallisce per qualunque motivo usa il file.
func (k KeyringTokens) SetToken(host, token string) error {
	if err := keyring.Set(KeyringService, host, token); err == nil {
		// Nel file non deve restare un token vecchio.
		return k.file().clearFileToken(host)
	}
	return k.file().SetToken(host, token)
}

// DeleteToken toglie il token da entrambi i posti.
func (k KeyringTokens) DeleteToken(host string) error {
	// Portachiavi non disponibile o voce assente: lì non c'è niente da togliere.
	_ = keyring.Delete(KeyringService, host)
	return k.file().DeleteToken(host)
}

// clearFileToken svuota il token nel file senza creare l'istanza se non c'è.
func (f FileTokens) clearFileToken(host string) error {
	hc, ok, err := f.Cfg.Host(host)
	if err != nil || !ok || hc.Token == "" {
		return err
	}
	return f.DeleteToken(host)
}
