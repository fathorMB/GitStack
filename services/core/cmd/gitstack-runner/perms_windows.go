//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"os/user"
)

// protectFile lascia a path solo l'utente che lo scrive (GIT-526). Su Windows il modo 0600 di
// os.WriteFile non limita nulla: il file eredita gli accessi della cartella, e fuori da
// %AppData% (una cartella scelta con --config, C:\ProgramData) lo leggono anche gli altri
// utenti del PC. icacls toglie l'ereditarietà e concede il controllo totale a un solo utente e toglie Everyone, Users e Authenticated Users.
func protectFile(path string) error {
	u, err := user.Current()
	if err != nil {
		return err
	}
	out, err := exec.Command("icacls", path, "/inheritance:r", "/grant:r", u.Username+":(F)",
		// e toglie le voci esplicite di Everyone, Users e Authenticated Users (per SID, valgono in ogni lingua)
		"/remove:g", "*S-1-1-0", "*S-1-5-32-545", "*S-1-5-11").CombinedOutput()
	if err != nil {
		return fmt.Errorf("icacls: %v: %s", err, out)
	}
	return nil
}
