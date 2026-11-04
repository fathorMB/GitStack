package cirunner

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Segreti e variabili del repo per act (GIT-526). Il runner passava a act solo GITHUB_TOKEN: i
// segreti del repo e le variabili del task (`secrets.X`, `vars.X`) restavano vuoti, e con loro
// ogni firma o login dei workflow (es. TAURI_SIGNING_PRIVATE_KEY). Non vanno nell'ambiente di
// act, perché i passi in modalità host ne ereditano l'ambiente e vedrebbero tutti i segreti, non
// solo quelli che citano; né sulla riga di comando (la vede chiunque sul PC con la lista dei
// processi, e su Windows il limite è 32 KB). Vanno in un file letto da act (--secret-file,
// --var-file), scritto 0600 nella cartella di lavoro e tolto a fine job.

// envNameRe: nomi accettati. Un nome fuori da questa forma non passa il filtro del file dotenv di act.
var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// dotenvQuote scrive v tra virgolette doppie nel dialetto dei file .env di act (godotenv): a-capo,
// ritorno a capo, barra inversa, virgolette e «$» (che altrimenti espande ${VAR}) con escape, così il
// valore arriva identico anche se è una chiave su più righe, con CRLF o con caratteri non ASCII.
func dotenvQuote(v string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, "\n", `\n`, "\r", `\r`)
	return `"` + r.Replace(v) + `"`
}

// writeDotenv scrive kv in path (0600), ordinato per nome. skip sono nomi da non scrivere.
// Ritorna i nomi scartati perché non validi: il chiamante li segnala nel log del job.
func writeDotenv(path string, kv map[string]string, skip ...string) (dropped []string, err error) {
	skipSet := map[string]bool{}
	for _, s := range skip {
		skipSet[s] = true
	}
	keys := make([]string, 0, len(kv))
	for k := range kv {
		if skipSet[k] {
			continue
		}
		if !envNameRe.MatchString(k) {
			dropped = append(dropped, k)
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	sort.Strings(dropped)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, dotenvQuote(kv[k]))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	// Prima si toglie un file rimasto da un job interrotto: WriteFile non cambia i permessi di uno esistente.
	_ = os.Remove(path)
	return dropped, os.WriteFile(path, []byte(b.String()), 0o600)
}

// actEnvFiles scrive i file dotenv del job e ritorna gli argomenti di act che li usano, più la
// funzione che li toglie. Passa sempre anche --env-file: act altrimenti legge `.env`, `.secrets`
// e `.vars` dalla cartella corrente, cioè dal codice del repo (un `.env` di Vite o Tauri diventava
// l'ambiente del job, e un `.secrets` committato i suoi segreti). Così contano solo i file del runner.
func actEnvFiles(cfg Config, rep *reporter, secrets, vars map[string]string) ([]string, func(), error) {
	dir := filepath.Join(cfg.WorkDir, "actenv")
	secretsF, varsF, envF := filepath.Join(dir, "secrets.env"), filepath.Join(dir, "vars.env"), filepath.Join(dir, "empty.env")
	cleanup := func() { _ = os.RemoveAll(dir) }
	dropped, err := writeDotenv(secretsF, secrets, "GITHUB_TOKEN")
	if err == nil {
		var droppedVars []string
		droppedVars, err = writeDotenv(varsF, vars)
		dropped = append(dropped, droppedVars...)
	}
	if err == nil {
		_, err = writeDotenv(envF, nil)
	}
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	if len(dropped) > 0 {
		rep.line("avviso: segreti o variabili con un nome non valido non sono passati al job: " + strings.Join(dropped, ", "))
	}
	return []string{"--secret-file", secretsF, "--var-file", varsF, "--env-file", envF}, cleanup, nil
}
