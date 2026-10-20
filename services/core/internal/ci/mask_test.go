package ci

import (
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"strings"
	"testing"
)

func TestMaskerCodificheBanali(t *testing.T) {
	const secret = "s3cr3t/Valore+Lungo=1"
	m := NewMasker(secret)
	b64 := base64.StdEncoding.EncodeToString([]byte(secret))
	cases := map[string]string{
		"in chiaro":      "token=" + secret + " fine",
		"base64":         "Authorization: Basic " + b64,
		"base64 url":     base64.URLEncoding.EncodeToString([]byte(secret)),
		"base64 senza =": strings.TrimRight(b64, "="),
		"percentuale":    url.QueryEscape(secret),
		"esadecimale":    hex.EncodeToString([]byte(secret)),
		"al contrario":   reverse(secret),
	}
	for name, line := range cases {
		got := m.Mask(line)
		if strings.Contains(got, secret) || strings.Contains(got, b64) || !strings.Contains(got, MaskedPlaceholder) {
			t.Errorf("%s: %q -> %q", name, line, got)
		}
	}
	// base64 con prefissi di 1 e 2 byte (il segreto dentro un testo più lungo).
	for _, prefix := range []string{"a:", "ab:", "x"} {
		line := base64.StdEncoding.EncodeToString([]byte(prefix + secret + "-coda"))
		if got := m.Mask(line); got == line {
			t.Errorf("prefisso %q non mascherato: %q", prefix, got)
		}
	}
	if got := m.Mask("niente da vedere"); got != "niente da vedere" {
		t.Errorf("riga normale alterata: %q", got)
	}
}

func TestMaskerRigheESegretiCorti(t *testing.T) {
	m := NewMasker("riga-uno-segreta\nriga-due-segreta", "ab", "")
	if got := m.Mask("x riga-due-segreta y"); strings.Contains(got, "segreta") {
		t.Errorf("riga di un segreto multi-riga: %q", got)
	}
	if got := m.Mask("abab"); got != "abab" {
		t.Errorf("un valore di 2 caratteri non si maschera: %q", got)
	}
	var nilMasker *Masker
	if nilMasker.Mask("x") != "x" {
		t.Error("masker nil")
	}
}

func TestCapPermissions(t *testing.T) {
	wide := map[string]string{"contents": "write", "packages": "write", "actions": "write"}
	allWrite := RepoTokenDefaults{LevelWrite, LevelWrite, LevelWrite}
	// Chi ha read non ottiene write, nemmeno con un repo permissivo e un workflow che lo chiede.
	c := CapPermissions(wide, allWrite, LevelRead)
	for k, v := range c.Permissions {
		if v != LevelRead {
			t.Errorf("read: %s = %s", k, v)
		}
	}
	if !c.CappedByActor {
		t.Error("cappedByActor atteso")
	}
	// Chi ha write ottiene write fin dove il repo lo consente.
	c = CapPermissions(wide, RepoTokenDefaults{LevelWrite, LevelRead, LevelNone}, LevelWrite)
	if c.Permissions["contents"] != LevelWrite || c.Permissions["packages"] != LevelRead || c.Permissions["actions"] != LevelNone || c.CappedByActor {
		t.Errorf("write: %+v", c)
	}
	// Nessun accesso: none. Senza attore (schedule): solo il tetto del repo.
	if c = CapPermissions(wide, allWrite, LevelNone); c.Permissions["contents"] != LevelNone {
		t.Errorf("none: %+v", c)
	}
	if c = CapPermissions(wide, DefaultRepoTokenDefaults(), ""); c.Permissions["contents"] != LevelRead || c.CappedByActor {
		t.Errorf("schedule: %+v", c)
	}
	// Senza permissions: il default è read; valori sconosciuti non allargano.
	if c = CapPermissions(nil, allWrite, LevelWrite); c.Permissions["contents"] != LevelRead {
		t.Errorf("default: %+v", c)
	}
	if c = CapPermissions(map[string]string{"contents": "admin"}, allWrite, LevelWrite); c.Permissions["contents"] != LevelRead {
		t.Errorf("valore sconosciuto: %+v", c)
	}
}
