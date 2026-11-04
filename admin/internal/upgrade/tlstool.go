package upgrade

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// TLSScriptFile è lo script nel sorgente del commit di destinazione.
	TLSScriptFile = "deploy/gitstack-tls.sh"
	// DefaultTLSToolPath è dove lo installa deploy/install.sh (install_tls_tool).
	DefaultTLSToolPath = "/usr/local/sbin/gitstack-tls"
	// maxTLSScript è il limite di lettura dello script (oggi poche decine di KB).
	maxTLSScript = 4 << 20
)

// installTLSTool installa il gitstack-tls della versione di destinazione come
// install_tls_tool di deploy/install.sh: mode 0755, scritto in un file
// temporaneo accanto alla destinazione e poi rename (atomico). Lo script arriva
// dal sorgente del commit, già risolto e scaricato per il chart (GIT-543).
// Non è mai fatale: se non si può installare, lo script vecchio resta e il
// messaggio dice cosa fare.
func (o *Options) installTLSTool(res *Result, t *Target) {
	dst := o.TLSToolPath
	if dst == "" {
		dst = DefaultTLSToolPath
	}
	if len(t.TLSScript) == 0 && t.TLSNote != "" {
		res.add("gitstack-tls NON aggiornato: %s; resta %s com'era.", t.TLSNote, dst)
		return
	}
	if len(t.TLSScript) == 0 {
		res.add("gitstack-tls NON aggiornato: %s non è nel sorgente di %s; resta %s com'era.", TLSScriptFile, t.Tag, dst)
		return
	}
	if !bytes.HasPrefix(t.TLSScript, []byte("#!")) {
		res.add("gitstack-tls NON aggiornato: %s di %s non è uno script; resta %s com'era.", TLSScriptFile, t.Tag, dst)
		return
	}
	if cur, err := os.ReadFile(dst); err == nil && bytes.Equal(cur, t.TLSScript) {
		o.logf("    gitstack-tls già aggiornato (%s)", dst)
		return
	} else if err != nil && os.IsNotExist(err) {
		// Non installato (installazione senza HTTPS gestito): non lo si crea.
		o.logf("    %s non esiste: gitstack-tls non è in uso su questo host, non lo installo", dst)
		return
	}
	if err := writeAtomic(dst, t.TLSScript, 0o755); err != nil {
		res.add("gitstack-tls NON aggiornato (%v): resta %s com'era. Installalo a mano: sudo install -m 0755 deploy/gitstack-tls.sh %s (dal sorgente di %s).", err, dst, dst, t.Tag)
		return
	}
	res.add("gitstack-tls aggiornato a quello di %s (%s).", t.Tag, dst)
}

func writeAtomic(dst string, b []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".gitstack-tls-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		return err
	}
	if err := os.Rename(name, dst); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	ok = true
	return nil
}
