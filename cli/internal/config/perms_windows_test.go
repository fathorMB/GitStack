//go:build windows

package config

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// assertPrivate legge la DACL: deve essere protetta (non eredita) e avere
// voci di accesso consentito solo per l'utente corrente.
func assertPrivate(t *testing.T, path string, dir bool) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	ctrl, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if ctrl&windows.SE_DACL_PROTECTED == 0 {
		t.Errorf("%s: la DACL eredita dalla cartella padre", path)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("%s: DACL assente (%v): accessibile a tutti", path, err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	// Un file ha una voce; una cartella, con l'ereditarietà, può averne due
	// (una per sé, una solo-ereditabile): tutte per l'utente corrente.
	max := 1
	if dir {
		max = 2
	}
	if n := int(dacl.AceCount); n < 1 || n > max {
		t.Errorf("%s: %d voci nella DACL, attese al massimo %d", path, n, max)
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatal(err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			t.Errorf("%s: voce %d di tipo %d", path, i, ace.Header.AceType)
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.Equals(user.User.Sid) {
			t.Errorf("%s: voce per %s, non per l'utente corrente %s", path, sid, user.User.Sid)
		}
	}
}
