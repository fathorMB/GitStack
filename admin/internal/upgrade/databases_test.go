package upgrade

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	pgDigest16 = "sha256:0cefaabd882e42bcb2a2f3dc56d3ce4770560ba62fd7f197f1a2e008fd5731ab"
	pgDigest17 = "sha256:266d3c257594e195b309ae9d1805e54c788f1d7b375f56bf6c367aac6c917e7b"
	pgRepo     = "registry.test/postgres"
	pgRef16    = pgRepo + "@" + pgDigest16
	pgRef17    = pgRepo + "@" + pgDigest17
)

// pgEntry è una riga della tabella major → immagine come la rende il chart.
type pgEntry struct {
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	Digest     string `json:"digest"`
}

// renderDB è il Deployment di core come lo rende helm con i database accesi (GIT-565): le variabili che
// il controller riceve. È l'unica fonte di abilitazione e tabella per `gitstack upgrade`.
func renderDB(table map[string]pgEntry) string {
	b, _ := json.Marshal(table)
	return "\n---\nkind: Deployment\nmetadata:\n  name: gitstack-core\nspec:\n  template:\n    spec:\n      containers:\n        - name: core\n          env:\n" +
		"            - name: GITSTACK_CORE_DATABASES_ENABLED\n              value: \"true\"\n" +
		"            - name: GITSTACK_CORE_DATABASES_POSTGRES_IMAGES\n              value: '" + string(b) + "'\n"
}

func defaultTable(d16, d17 string) map[string]pgEntry {
	return map[string]pgEntry{
		"16": {Repository: pgRepo, Tag: "16-alpine", Digest: d16},
		"17": {Repository: pgRepo, Tag: "17-alpine", Digest: d17},
	}
}

// signImages aggiunge le immagini all'elenco di quelle firmate dal cosign finto.
func signImages(t *testing.T, w *world, identity string, refs ...string) {
	t.Helper()
	f, err := os.OpenFile(w.imgSigners, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	for _, r := range refs {
		if _, err := f.WriteString(r + " " + identity + "\n"); err != nil {
			t.Fatal(err)
		}
	}
}

// writeRoot scrive ca.crt, ca.key e tls.conf in dir (la radice dell'istanza sull'host).
func writeRoot(t *testing.T, dir string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "CA di prova"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(10 * 365 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kd, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "ca.crt"), string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})))
	mustWrite(t, filepath.Join(dir, "ca.key"), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd})))
	mustWrite(t, filepath.Join(dir, "tls.conf"), "TLS_NAMES=git.acme.test\nTLS_IPS=10.0.0.5\n")
}

// dbWorld: il chart nuovo rende i database accesi con la tabella di default; il chart attuale non li ha.
func dbWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	w.helm.dbNew = renderDB(defaultTable(pgDigest16, pgDigest17))
	w.o.TLSDir = t.TempDir()
	writeRoot(t, w.o.TLSDir)
	return w
}

func calledWith(t *testing.T, ref string) bool {
	t.Helper()
	for _, c := range cosignCalls(t) {
		if strings.Contains(c, ref) {
			return true
		}
	}
	return false
}

// refused lancia Run e pretende un RefusedError che contenga want, senza nessun helm upgrade.
func refusedNoUpgrade(t *testing.T, w *world, want ...string) {
	t.Helper()
	res, err := Run(context.Background(), w.o)
	if err == nil || res.Outcome != Refused {
		t.Fatalf("atteso un rifiuto, res=%+v err=%v", res, err)
	}
	for _, s := range want {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("il messaggio %q non contiene %q", err.Error(), s)
		}
	}
	if w.helm.upgraded || w.helm.upgradeValues != "" {
		t.Error("helm upgrade lanciato nonostante il rifiuto")
	}
	if len(w.cl.applied) != 0 {
		t.Errorf("Secret creati nonostante il rifiuto: %v", w.cl.applied)
	}
}

// DB5: i riferimenti nuovi della tabella si verificano con cosign prima di applicare il chart.
func TestDatabaseDigests_FirmaVerificataPoiChartApplicato(t *testing.T) {
	w := dbWorld(t)
	signImages(t, w, validIdentity, pgRef16, pgRef17)
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	for _, ref := range []string{pgRef16, pgRef17} {
		if !calledWith(t, ref) {
			t.Errorf("cosign non ha verificato %s: %v", ref, cosignCalls(t))
		}
	}
}

func TestDatabaseDigests_FirmaMancanteFermaUpgrade(t *testing.T) {
	w := dbWorld(t)
	signImages(t, w, validIdentity, pgRef16) // la 17 non è firmata
	refusedNoUpgrade(t, w, "immagine postgres", pgRef17, "major 17")
}

func TestDatabaseDigests_FirmaDiAltraIdentitaFermaUpgrade(t *testing.T) {
	w := dbWorld(t)
	signImages(t, w, validIdentity, pgRef16)
	signImages(t, w, "https://github.com/altro/repo/.github/workflows/ci.yml@refs/heads/main", pgRef17)
	refusedNoUpgrade(t, w, "immagine postgres", pgRef17)
}

func TestDatabaseDigests_DryRunVerificaComunque(t *testing.T) {
	w := dbWorld(t)
	w.o.DryRun = true
	refusedNoUpgrade(t, w, "immagine postgres")
}

// Database spenti (bool false): il rendering non ha le variabili, niente verifica e niente Secret.
func TestDatabaseDigests_SpentiNientePrimaNienteSecret(t *testing.T) {
	w := dbWorld(t)
	w.helm.dbNew = ""
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if calledWith(t, pgRepo) || len(w.cl.applied) != 0 {
		t.Errorf("database spenti: cosign=%v secret=%v", cosignCalls(t), w.cl.applied)
	}
}

func TestDatabaseDigests_EccezioneN9(t *testing.T) {
	w := dbWorld(t)
	w.o.V5.AllowUnsigned = true
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if calledWith(t, pgRef16) {
		t.Error("con l'eccezione N9 non si chiama cosign")
	}
}

// Il riferimento effettivo è quello del rendering: un override non firmato ferma l'upgrade, e il default
// sostituito non si verifica al suo posto.
func TestDatabaseDigests_RiferimentoEffettivoNonFirmato(t *testing.T) {
	ov := "sha256:" + strings.Repeat("b", 64)
	w := dbWorld(t)
	w.helm.dbNew = renderDB(defaultTable(ov, pgDigest17))
	signImages(t, w, validIdentity, pgRef16, pgRef17)
	refusedNoUpgrade(t, w, "immagine postgres", pgRepo+"@"+ov, "major 16")
	if calledWith(t, pgRef16) {
		t.Error("il digest sostituito non va verificato al posto dell'effettivo")
	}
	w2 := dbWorld(t)
	w2.helm.dbNew = renderDB(defaultTable(ov, pgDigest17))
	signImages(t, w2, validIdentity, pgRepo+"@"+ov, pgRef17)
	res, err := Run(context.Background(), w2.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

// Stesso digest ma repository diverso: è un riferimento nuovo.
func TestDatabaseDigests_StessoDigestRepositoryDiverso(t *testing.T) {
	w := dbWorld(t)
	w.helm.dbOld = renderDB(defaultTable(pgDigest16, pgDigest17))
	tbl := defaultTable(pgDigest16, pgDigest17)
	e := tbl["16"]
	e.Repository = "altro.test/pg"
	tbl["16"] = e
	w.helm.dbNew = renderDB(tbl)
	signImages(t, w, validIdentity, pgRef17)
	refusedNoUpgrade(t, w, "altro.test/pg@"+pgDigest16)
}

// Solo i riferimenti nuovi: ciò che il chart attuale rendeva già si riusa.
func TestDatabaseDigests_RiferimentoInvariatoRiuso(t *testing.T) {
	w := dbWorld(t)
	w.helm.dbOld = renderDB(defaultTable(pgDigest16, pgDigest17))
	w.helm.dbNew = renderDB(defaultTable(pgDigest16, "sha256:"+strings.Repeat("a", 64)))
	signImages(t, w, validIdentity, pgRepo+"@sha256:"+strings.Repeat("a", 64))
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if calledWith(t, pgRef16) {
		t.Error("riferimento invariato: non si riverifica")
	}
	if !calledWith(t, "sha256:"+strings.Repeat("a", 64)) {
		t.Error("il digest nuovo va verificato")
	}
}

// Input non supportati: rifiutati, mai ignorati.
func TestDatabaseRender_InputNonSupportatiRifiutati(t *testing.T) {
	enabledNoImages := "\n---\nkind: Deployment\nmetadata:\n  name: gitstack-core\nspec:\n  template:\n    spec:\n      containers:\n        - name: core\n          env:\n            - name: GITSTACK_CORE_DATABASES_ENABLED\n              value: \"true\"\n"
	bad := defaultTable(pgDigest16, "latest")
	for name, render := range map[string]string{
		"acceso senza tabella":   enabledNoImages,
		"digest non sha256":      renderDB(bad),
		"tabella vuota":          renderDB(map[string]pgEntry{}),
		"repository mancante":    renderDB(map[string]pgEntry{"16": {Digest: pgDigest16}}),
		"manifest non leggibili": "\n---\n: [\n",
	} {
		t.Run(name, func(t *testing.T) {
			w := dbWorld(t)
			w.helm.dbNew = render
			refusedNoUpgrade(t, w)
		})
	}
}

// Il valore di abilitazione si legge come lo rende Helm, senza distinguere maiuscole.
func TestDatabaseRender_AbilitazioneComeHelm(t *testing.T) {
	for _, v := range []string{"true", "TRUE", "True"} {
		out := strings.Replace(renderDB(defaultTable(pgDigest16, pgDigest17)), `value: "true"`, `value: "`+v+`"`, 1)
		st, err := databaseStateFromManifests([]byte(out))
		if err != nil || !st.Enabled || len(st.Images) != 2 {
			t.Errorf("%s: st=%+v err=%v", v, st, err)
		}
	}
	st, err := databaseStateFromManifests([]byte("kind: Service\nmetadata:\n  name: x\n"))
	if err != nil || st.Enabled {
		t.Errorf("senza variabili: st=%+v err=%v", st, err)
	}
	// Anche solo il ClusterRole modello senza le variabili conta come acceso (e senza tabella è un errore).
	if _, err := databaseStateFromManifests([]byte("kind: ClusterRole\nmetadata:\n  name: gitstack-db-workload\n")); err == nil {
		t.Error("ClusterRole dei database senza tabella: doveva essere rifiutato")
	}
}

// L'upgrade di un'installazione esistente crea intermedia e chiave di dump se mancano e non le rigenera.
func TestDatabaseSecrets_CreatiSeMancanoNonRigenerati(t *testing.T) {
	w := dbWorld(t)
	signImages(t, w, validIdentity, pgRef16, pgRef17)
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(w.cl.applied) != 2 || !strings.HasSuffix(w.cl.applied[0], "-db-dump-key") || !strings.HasSuffix(w.cl.applied[1], "-db-ca") {
		t.Fatalf("Secret creati: %v", w.cl.applied)
	}
	before := map[string]string{}
	for _, s := range w.cl.secrets {
		before[s.Name] = string(s.Raw)
	}
	w.o.Cfg.ImageTag = "sha-" + oldSHA
	res, err = Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("secondo upgrade: res=%+v err=%v", res, err)
	}
	if len(w.cl.applied) != 2 {
		t.Errorf("il secondo upgrade ha riscritto dei Secret: %v", w.cl.applied)
	}
	for _, s := range w.cl.secrets {
		if before[s.Name] != string(s.Raw) {
			t.Errorf("il Secret %s è cambiato", s.Name)
		}
	}
}

func TestDatabaseSecrets_SenzaRadiceAvvisaEProsegue(t *testing.T) {
	w := dbWorld(t)
	w.o.TLSDir = t.TempDir() // certificato del cliente: nessuna CA interna sull'host
	signImages(t, w, validIdentity, pgRef16, pgRef17)
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if !strings.Contains(strings.Join(res.Summary, "\n"), "CA intermedia NON creata") {
		t.Errorf("manca l'avviso nel riepilogo: %v", res.Summary)
	}
	if len(w.cl.applied) != 1 || !strings.HasSuffix(w.cl.applied[0], "-db-dump-key") {
		t.Errorf("la chiave di dump va creata comunque: %v", w.cl.applied)
	}
}

// DB6 (GIT-557): una release che toglie dalla tabella una major ancora usata da un database è rifiutata,
// prima di qualunque modifica (nemmeno il backup preventivo o i Secret).
func TestDatabaseMajors_ReleaseCheTogliUnaMajorInUsoERifiutata(t *testing.T) {
	w := dbWorld(t)
	signImages(t, w, validIdentity, pgRef16, pgRef17)
	w.cl.majors = "16\n17\n"
	w.helm.dbNew = renderDB(map[string]pgEntry{"17": {Repository: pgRepo, Tag: "17-alpine", Digest: pgDigest17}})
	refusedNoUpgrade(t, w, "major PostgreSQL 16", "DB6")
	for _, c := range w.cl.calls {
		if c == "pg_dump" {
			t.Error("il backup preventivo non deve partire se la release è rifiutata")
		}
	}
}

func TestDatabaseMajors_DryRunRifiutaComunque(t *testing.T) {
	w := dbWorld(t)
	signImages(t, w, validIdentity, pgRef16, pgRef17)
	w.o.DryRun = true
	w.cl.majors = "16\n"
	w.helm.dbNew = renderDB(map[string]pgEntry{"17": {Repository: pgRepo, Tag: "17-alpine", Digest: pgDigest17}})
	refusedNoUpgrade(t, w, "major PostgreSQL 16")
}

// La major tolta ma non usata da nessun database non blocca l'aggiornamento.
func TestDatabaseMajors_MajorNonInUsoSiPuoTogliere(t *testing.T) {
	w := dbWorld(t)
	signImages(t, w, validIdentity, pgRef17)
	w.cl.majors = "17\n"
	w.helm.dbNew = renderDB(map[string]pgEntry{"17": {Repository: pgRepo, Tag: "17-alpine", Digest: pgDigest17}})
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestDatabaseMajors_TabellaCompletaPassa(t *testing.T) {
	w := dbWorld(t)
	signImages(t, w, validIdentity, pgRef16, pgRef17)
	w.cl.majors = "16\n17\n"
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

// Lettura impossibile: l'aggiornamento non la ignora.
func TestDatabaseMajors_LetturaImpossibileFerma(t *testing.T) {
	w := dbWorld(t)
	signImages(t, w, validIdentity, pgRef16, pgRef17)
	w.cl.majorsErr = errors.New("pod non raggiungibile")
	refusedNoUpgrade(t, w, "non riesco a leggere le major")
}

// Funzione spenta nella destinazione (o già spenta): spegnere conserva i database, quindi una release non può
// togliere una major ancora in uso. La tabella si ricava da Helm con databases.enabled=true forzato.
func spentiWorld(t *testing.T, forced map[string]pgEntry) *world {
	t.Helper()
	w := dbWorld(t)
	w.helm.dbNew = ""
	w.helm.dbForced = renderDB(forced)
	return w
}

func noPgDump(t *testing.T, w *world) {
	t.Helper()
	for _, c := range w.cl.calls {
		if c == "pg_dump" {
			t.Error("il backup preventivo non deve partire se la release è rifiutata")
		}
	}
}

func TestDatabaseMajors_SpentiMajorInUsoTolta(t *testing.T) {
	w := spentiWorld(t, map[string]pgEntry{"17": {Repository: pgRepo, Tag: "17-alpine", Digest: pgDigest17}})
	w.cl.majors = "16\n"
	refusedNoUpgrade(t, w, "major PostgreSQL 16", "DB6")
	noPgDump(t, w)
}

func TestDatabaseMajors_SpentiDryRunRifiutaComunque(t *testing.T) {
	w := spentiWorld(t, map[string]pgEntry{"17": {Repository: pgRepo, Tag: "17-alpine", Digest: pgDigest17}})
	w.o.DryRun = true
	w.cl.majors = "16\n"
	refusedNoUpgrade(t, w, "major PostgreSQL 16")
	noPgDump(t, w)
}

func TestDatabaseMajors_SpentiTabellaChePreservaLaMajorPassa(t *testing.T) {
	w := spentiWorld(t, defaultTable(pgDigest16, pgDigest17))
	w.cl.majors = "16\n"
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestDatabaseMajors_SpentiMajorNonUsataSiPuoTogliere(t *testing.T) {
	w := spentiWorld(t, map[string]pgEntry{"17": {Repository: pgRepo, Tag: "17-alpine", Digest: pgDigest17}})
	w.cl.majors = "17\n"
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

// Nessun database: nessun rendering forzato necessario, l'upgrade da spento passa.
func TestDatabaseMajors_SpentiSenzaDatabasePassa(t *testing.T) {
	w := spentiWorld(t, nil)
	w.helm.dbForcedErr = errors.New("non deve servire")
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

// Major in uso ma tabella della release non ricavabile: rifiuto, mai silenzio.
func TestDatabaseMajors_SpentiTabellaNonRicavabileFerma(t *testing.T) {
	w := spentiWorld(t, nil)
	w.helm.dbForced = ""
	w.helm.dbForcedErr = errors.New("helm template fallito")
	w.cl.majors = "16\n"
	refusedNoUpgrade(t, w, "non riesco a ricavare la tabella")
}

func TestDatabaseMajors_SpentiLetturaImpossibileFerma(t *testing.T) {
	w := spentiWorld(t, defaultTable(pgDigest16, pgDigest17))
	w.cl.majorsErr = errors.New("pod non raggiungibile")
	refusedNoUpgrade(t, w, "non riesco a leggere le major")
}

func TestParseMajors(t *testing.T) {
	got := parseMajors("17\r\n16\n\nx\n")
	if len(got) != 2 || got[0] != 16 || got[1] != 17 {
		t.Errorf("parseMajors = %v", got)
	}
}
