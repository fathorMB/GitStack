package mcp

import (
	"encoding/json"
	"testing"
)

// GIT-556 (v3-d): gli strumenti database_* escono dal contratto. Le letture sono di sola lettura, le scritture che
// distruggono o espongono dati (creare, eliminare, ripristinare, scollegare, accendere la LAN, aggiornare la minor)
// sono distruttive; ruotare le credenziali, collegare un'app e avviare un backup sono scritture non distruttive,
// come app_deploy o app_restart.
func TestStrumentiMCPDeiDatabaseConLeAnnotazioniGiuste(t *testing.T) {
	c := newClient(t)
	l := c.send("tools/list", nil)
	res := c.run(false)
	var ls listed
	_ = json.Unmarshal(res[l].Result, &ls)
	by := map[string]int{}
	for k, tl := range ls.Tools {
		by[tl.Name] = k
	}
	want := map[string]string{
		"database_list": "read", "database_get": "read", "database_link_list": "read", "database_backup_list": "read",
		"database_lan_get": "read", "database_logs": "read", "database_metrics": "read",
		"database_create": "destructive", "database_delete": "destructive", "database_link_delete": "destructive",
		"database_backup_restore": "destructive", "database_lan_set": "destructive", "database_upgrade_minor": "destructive",
		"database_credentials_rotate": "write", "database_link_create": "write", "database_backup_create": "write",
	}
	for name, kind := range want {
		i, ok := by[name]
		if !ok {
			t.Errorf("strumento %s assente", name)
			continue
		}
		a := ls.Tools[i].Annotations
		switch kind {
		case "read":
			if !a.ReadOnlyHint || a.DestructiveHint {
				t.Errorf("%s: %+v, attesa lettura", name, a)
			}
		case "write":
			if a.ReadOnlyHint || a.DestructiveHint {
				t.Errorf("%s: %+v, attesa scrittura non distruttiva", name, a)
			}
		case "destructive":
			if a.ReadOnlyHint || !a.DestructiveHint {
				t.Errorf("%s: %+v, attesa distruttiva", name, a)
			}
		}
	}
}

// Ogni strumento database_* del server ha una riga nella tabella: un'operazione nuova nel contratto obbliga a
// decidere se è di lettura, scrittura o distruttiva.
func TestStrumentiMCPDeiDatabaseSonoTuttiClassificati(t *testing.T) {
	known := map[string]bool{
		"database_list": true, "database_get": true, "database_link_list": true, "database_backup_list": true,
		"database_lan_get": true, "database_logs": true, "database_metrics": true, "database_create": true,
		"database_delete": true, "database_link_delete": true, "database_backup_restore": true, "database_lan_set": true,
		"database_upgrade_minor": true, "database_credentials_rotate": true, "database_link_create": true, "database_backup_create": true,
	}
	n := 0
	for _, tl := range Tools {
		if len(tl.Name) > 9 && tl.Name[:9] == "database_" {
			n++
			if !known[tl.Name] {
				t.Errorf("strumento %s non classificato nel test delle annotazioni", tl.Name)
			}
		}
	}
	if n != len(known) {
		t.Errorf("strumenti database_*: %d, attesi %d", n, len(known))
	}
}
