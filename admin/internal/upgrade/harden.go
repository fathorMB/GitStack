package upgrade

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Hardening di k3s (GIT-228, F2 del registro ASVS in docs/security/asvs-l2.md).
//
// Le installazioni create prima di GIT-228 avviano k3s con
// --write-kubeconfig-mode 644 e senza --secrets-encryption: il kubeconfig
// (cluster-admin) è leggibile da ogni account e i Secret stanno in chiaro nel
// datastore. `gitstack upgrade` le sistema:
//
//  1. riscrive l'unità k3s.service (modo 600, --secrets-encryption),
//     tenendone una copia;
//  2. daemon-reload, restart di k3s e attesa che i servizi siano sani;
//  3. `k3s secrets-encrypt rotate-keys`, che riscrive in cifrato i Secret
//     già presenti, e verifica con `k3s secrets-encrypt status`;
//  4. se qualcosa va storto prima che la cifratura sia attiva, rimette
//     l'unità com'era e riavvia k3s.
//
// Il backup preventivo precede sempre questo passo (Run).

// k3sUnitName è l'unità systemd creata dallo script di k3s.
const k3sUnitName = "k3s.service"

var (
	// --write-kubeconfig-mode 644, --write-kubeconfig-mode=644, anche con
	// apici e a capo con backslash fra nome e valore (formato dello script k3s).
	kubeconfigModeRe = regexp.MustCompile(`(--write-kubeconfig-mode['"]?(?:=|[ \t]*(?:\\\r?\n)?[ \t]+)['"]?)(\d{3,4})`)
	secretsEncRe     = regexp.MustCompile(`--secrets-encryption(=true)?['"]?(\s|$)`)
	secretsEncOffRe  = regexp.MustCompile(`--secrets-encryption=false`)
	serverLineRe     = regexp.MustCompile(`(?m)^([ \t]+)['"]?server['"]?[ \t]*\\\r?\n`)
	serverInlineRe   = regexp.MustCompile(`(?m)^(ExecStart=[^\n\\]*\bserver\b[^\n\\]*)$`)
)

// HardenedUnit riscrive il testo di un'unità k3s.service: kubeconfig a 600 e
// --secrets-encryption. Restituisce il nuovo testo e cosa è cambiato; un testo
// già a posto torna identico.
func HardenedUnit(unit string) (out string, modeChanged, encChanged bool, err error) {
	if !strings.Contains(unit, "ExecStart=") {
		return unit, false, false, errors.New("unità k3s senza ExecStart")
	}
	out = unit
	if m := kubeconfigModeRe.FindStringSubmatch(out); m != nil && m[2] != "600" && m[2] != "0600" {
		out = kubeconfigModeRe.ReplaceAllString(out, "${1}600")
		modeChanged = true
	}
	if secretsEncOffRe.MatchString(out) {
		out = secretsEncOffRe.ReplaceAllString(out, "--secrets-encryption")
		encChanged = true
	}
	if !secretsEncRe.MatchString(out) {
		switch {
		case serverLineRe.MatchString(out):
			loc := serverLineRe.FindStringSubmatchIndex(out)
			indent := out[loc[2]:loc[3]]
			out = out[:loc[1]] + indent + "'--secrets-encryption' \\\n" + out[loc[1]:]
		case serverInlineRe.MatchString(out):
			out = serverInlineRe.ReplaceAllString(out, "$1 --secrets-encryption")
		default:
			return unit, false, false, errors.New("unità k3s in un formato non riconosciuto (nessun argomento «server»)")
		}
		encChanged = true
	}
	return out, modeChanged, encChanged, nil
}

// hardenPlan è quello che serve fare su k3s.
type hardenPlan struct {
	unitPath   string
	oldUnit    string
	newUnit    string
	modeFix    bool // l'unità ha un modo diverso da 600
	encEnable  bool // l'unità non ha --secrets-encryption
	chmodFile  bool // il kubeconfig ha permessi per gruppo/altri
	kubeconfig string
}

func (p *hardenPlan) needed() bool { return p != nil && (p.modeFix || p.encEnable || p.chmodFile) }

func (p *hardenPlan) describe() string {
	var parts []string
	if p.modeFix || p.chmodFile {
		parts = append(parts, "kubeconfig di k3s a 600 (oggi leggibile da altri account)")
	}
	if p.encEnable {
		parts = append(parts, "cifratura dei Secret a riposo (--secrets-encryption) con riavvio di k3s e re-cifratura dei Secret esistenti")
	}
	return strings.Join(parts, " e ")
}

// planHarden legge l'unità e il kubeconfig e decide. nil (senza errore) = k3s
// non gestito da systemd in questa cartella: niente da fare.
func (o *Options) planHarden() (*hardenPlan, error) {
	dir := o.SystemdDir
	if dir == "" {
		dir = "/etc/systemd/system"
	}
	p := &hardenPlan{unitPath: filepath.Join(dir, k3sUnitName), kubeconfig: o.Cfg.Kubeconfig}
	if b, err := os.ReadFile(p.unitPath); err == nil {
		p.oldUnit = string(b)
		out, mode, enc, herr := HardenedUnit(p.oldUnit)
		if herr != nil {
			return nil, refused("%s: %v. Applica a mano la procedura «Hardening di k3s» di docs/operations.md", p.unitPath, herr)
		}
		p.newUnit, p.modeFix, p.encEnable = out, mode, enc
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("lettura di %s: %w", p.unitPath, err)
	}
	if p.kubeconfig != "" {
		if st, err := os.Stat(p.kubeconfig); err == nil && st.Mode().Perm()&0o077 != 0 {
			p.chmodFile = true
		}
	}
	if !p.needed() {
		return nil, nil
	}
	return p, nil
}

// hardenK3s applica il piano. Va chiamato dopo il backup preventivo.
// Gli errori sono già nel formato del riepilogo; rolledBack dice se l'unità
// è stata rimessa com'era.
func (o *Options) hardenK3s(ctx context.Context, p *hardenPlan, res *Result) error {
	if p.newUnit != "" && p.newUnit != p.oldUnit {
		o.logf("==> Hardening di k3s: %s", p.describe())
		// Copia dell'unità com'era, accanto all'originale (systemd non la carica).
		if err := atomicWriteFile(p.unitPath+".pre-hardening", []byte(p.oldUnit), 0o644); err != nil { //nolint:gosec // copia di un'unità systemd
			return fmt.Errorf("copia di %s: %w", p.unitPath, err)
		}
		if err := atomicWriteFile(p.unitPath, []byte(p.newUnit), 0o644); err != nil { //nolint:gosec // unità systemd: 0644 come quella creata da k3s
			return fmt.Errorf("scrittura di %s: %w", p.unitPath, err)
		}
		restore := func() {
			if err := atomicWriteFile(p.unitPath, []byte(p.oldUnit), 0o644); err != nil { //nolint:gosec // idem
				o.logf("!! ripristino di %s non riuscito: %v", p.unitPath, err)
			}
			_, _ = o.Runner.Run(context.WithoutCancel(ctx), nil, "systemctl", "daemon-reload")
			_, _ = o.Runner.Run(context.WithoutCancel(ctx), nil, "systemctl", "restart", "k3s")
		}
		fail := func(step string, err error, out []byte) error {
			o.logf("!! %s: %v: %s", step, err, tail(string(out)))
			o.logf("==> Rimetto l'unità k3s com'era e riavvio k3s")
			restore()
			if werr := o.waitHealthy(context.WithoutCancel(ctx), o.timeout()); werr != nil {
				return fmt.Errorf("%s: %w; ripristino dell'unità fatto ma i servizi non sono tornati sani: %v", step, err, werr)
			}
			return fmt.Errorf("%s: %w (unità k3s rimessa com'era, servizi sani)", step, err)
		}
		if out, err := o.Runner.Run(ctx, nil, "systemctl", "daemon-reload"); err != nil {
			return fail("systemctl daemon-reload", err, out)
		}
		if out, err := o.Runner.Run(ctx, nil, "systemctl", "restart", "k3s"); err != nil {
			return fail("riavvio di k3s", err, out)
		}
		o.logf("==> Aspetto che k3s e tutti i servizi siano di nuovo sani (al massimo %s)", o.timeout())
		if err := o.waitHealthy(ctx, o.timeout()); err != nil {
			return fail("dopo il riavvio di k3s", err, nil)
		}
		if p.encEnable {
			out, err := o.Runner.Run(ctx, nil, "k3s", "secrets-encrypt", "status")
			if err != nil || !strings.Contains(string(out), "Encryption Status: Enabled") {
				if err == nil {
					err = errors.New("«k3s secrets-encrypt status» non dichiara la cifratura attiva")
				}
				return fail("verifica della cifratura", err, out)
			}
			// Da qui la cifratura è attiva: non si torna indietro togliendo il
			// flag (i Secret nuovi sarebbero illeggibili). Una rotazione non
			// riuscita si segnala, non si annulla.
			o.logf("==> Ricifro i Secret già presenti (k3s secrets-encrypt rotate-keys)")
			if out, err := o.Runner.Run(ctx, nil, "k3s", "secrets-encrypt", "rotate-keys"); err != nil {
				res.add("ATTENZIONE: la cifratura dei Secret è attiva, ma la ricifratura di quelli esistenti non è riuscita (%v: %s). Rilancia «sudo k3s secrets-encrypt rotate-keys» e controlla con «sudo k3s secrets-encrypt status».", err, tail(string(out)))
			} else if err := o.waitHealthy(ctx, o.timeout()); err != nil {
				return fmt.Errorf("dopo la rotazione delle chiavi: %w", err)
			}
		}
		res.add("k3s: kubeconfig a 600 e cifratura dei Secret attiva (unità %s aggiornata; copia precedente in %s.pre-hardening).", p.unitPath, p.unitPath)
	}
	// k3s riscrive il kubeconfig al riavvio col modo dell'unità; se non si è
	// riavviato (o il file è stato allargato dopo) lo si porta a 600 qui.
	if p.kubeconfig != "" {
		if st, err := os.Stat(p.kubeconfig); err == nil && st.Mode().Perm()&0o077 != 0 {
			if err := os.Chmod(p.kubeconfig, 0o600); err != nil {
				return fmt.Errorf("chmod 600 di %s: %w", p.kubeconfig, err)
			}
			res.add("k3s: %s portato a 600.", p.kubeconfig)
		}
	}
	return nil
}

func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}
