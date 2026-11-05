package upgrade

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
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
	pgRef16    = "registry.test/postgres@" + pgDigest16
	pgRef17    = "registry.test/postgres@" + pgDigest17
)

// dbValues è il values.yaml del chart con la tabella major → digest (databases.postgres.images).
func dbValues(d16, d17 string) string {
	return "NUOVO: 1\ndatabases:\n  enabled: false\n  postgres:\n    images:\n" +
		"      \"16\":\n        repository: registry.test/postgres\n        tag: 16-alpine\n        digest: " + d16 + "\n" +
		"      \"17\":\n        repository: registry.test/postgres\n        tag: 17-alpine\n        digest: " + d17 + "\n"
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

// dbWorld: database gestiti accesi (valori della release), tabella nel chart nuovo, radice sull'host.
func dbWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	w.values = dbValues(pgDigest16, pgDigest17)
	w.chartTgz = signedChartTgzValues(t, "name: gitstack\nversion: 0.1.0\n", signedTLS, w.values)
	w.helm.values = `{"databases":{"enabled":true}}`
	tlsDir := t.TempDir()
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
	mustWrite(t, filepath.Join(tlsDir, "ca.crt"), string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})))
	mustWrite(t, filepath.Join(tlsDir, "ca.key"), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd})))
	mustWrite(t, filepath.Join(tlsDir, "tls.conf"), "TLS_NAMES=git.acme.test\nTLS_IPS=10.0.0.5\n")
	w.o.TLSDir = tlsDir
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

// DB5: i digest nuovi della tabella si verificano con cosign prima di applicare il chart.
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
	runRefused(t, w, "immagine postgres", pgRef17, "major 17")
	if w.helm.upgradeValues != "" {
		t.Errorf("helm upgrade lanciato nonostante la firma mancante: %s", w.helm.upgradeValues)
	}
}

func TestDatabaseDigests_FirmaDiAltraIdentitaFermaUpgrade(t *testing.T) {
	w := dbWorld(t)
	signImages(t, w, validIdentity, pgRef16)
	signImages(t, w, "https://github.com/altro/repo/.github/workflows/ci.yml@refs/heads/main", pgRef17)
	runRefused(t, w, "immagine postgres", pgRef17)
}

func TestDatabaseDigests_DryRunVerificaComunque(t *testing.T) {
	w := dbWorld(t)
	w.o.DryRun = true
	runRefused(t, w, "immagine postgres")
	if len(w.cl.applied) != 0 {
		t.Errorf("--dry-run non deve creare Secret: %v", w.cl.applied)
	}
}

// Con i database già accesi si verificano solo i digest che la copia attuale del chart non aveva.
func TestDatabaseDigests_SoloQuelliNuovi(t *testing.T) {
	w := dbWorld(t)
	mustWrite(t, filepath.Join(w.chart, "values.yaml"), dbValues(pgDigest16, "sha256:"+strings.Repeat("a", 64)))
	signImages(t, w, validIdentity, pgRef17) // il 16 è lo stesso di prima: non serve una firma nuova
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if calledWith(t, pgRef16) {
		t.Error("il digest 16 era già nella tabella attuale: non si riverifica")
	}
	if !calledWith(t, pgRef17) {
		t.Error("il digest 17 è nuovo: va verificato")
	}
}

// Database spenti (DB23): la tabella c'è nel chart ma l'upgrade non verifica niente e non crea Secret.
func TestDatabaseDigests_SpentiNientePrimaNienteSecret(t *testing.T) {
	w := dbWorld(t)
	w.helm.values = `{}`
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if calledWith(t, "registry.test/postgres") {
		t.Error("con i database spenti non si verifica nessun digest postgres")
	}
	if len(w.cl.applied) != 0 {
		t.Errorf("Secret creati con i database spenti: %v", w.cl.applied)
	}
}

// Prima accensione con --set databases.enabled=true: tutta la tabella.
func TestDatabaseDigests_AccensioneConSet(t *testing.T) {
	w := dbWorld(t)
	w.helm.values = `{}`
	w.o.Sets = []string{"databases.enabled=true"}
	signImages(t, w, validIdentity, pgRef16, pgRef17)
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if !calledWith(t, pgRef16) || !calledWith(t, pgRef17) {
		t.Errorf("alla prima accensione si verifica tutta la tabella: %v", cosignCalls(t))
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

// L'upgrade di un'installazione esistente crea intermedia e chiave di dump se mancano e non le rigenera.
func TestDatabaseSecrets_CreatiSeMancanoNonRigenerati(t *testing.T) {
	w := dbWorld(t)
	signImages(t, w, validIdentity, pgRef16, pgRef17)
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if got := strings.Join(w.cl.applied, ","); got != "gitstack-db-dump-key,gitstack-db-ca" && got != "gs-db-dump-key,gs-db-ca" {
		// il nome dipende dalla release del config di prova
		if len(w.cl.applied) != 2 || !strings.HasSuffix(w.cl.applied[0], "-db-dump-key") || !strings.HasSuffix(w.cl.applied[1], "-db-ca") {
			t.Fatalf("Secret creati: %v", w.cl.applied)
		}
	}
	before := map[string]string{}
	for _, s := range w.cl.secrets {
		before[s.Name] = string(s.Raw)
	}

	// Secondo upgrade (stessa destinazione, dopo aver riportato la versione indietro): niente di nuovo.
	w.o.Cfg.ImageTag = "sha-" + oldSHA
	w.helm.values = `{"databases":{"enabled":true}}`
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

func TestDatabaseImagesFromValues(t *testing.T) {
	got, err := databaseImagesFromValues([]byte(dbValues(pgDigest16, pgDigest17)))
	if err != nil || len(got) != 2 || got[0].Major != "16" || got[1].Ref() != pgRef17 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if _, err := databaseImagesFromValues([]byte(dbValues("latest", pgDigest17))); err == nil {
		t.Error("un digest non sha256 doveva essere rifiutato")
	}
	if got, err := databaseImagesFromValues([]byte("NUOVO: 1\n")); err != nil || len(got) != 0 {
		t.Errorf("senza tabella: got=%+v err=%v", got, err)
	}
}

// --- tabella effettiva: valori conservati, -f e --set (rework 1, DB5) ---

var (
	ovDigest = "sha256:" + strings.Repeat("b", 64)
	ovRef16  = "registry.test/postgres@" + ovDigest
)

// Un override conservato nella release sostituisce il digest della 16: si verifica quello, non il default.
func TestDatabaseDigests_OverrideConservatoNonFirmatoFermaUpgrade(t *testing.T) {
	w := dbWorld(t)
	w.helm.values = `{"databases":{"enabled":true,"postgres":{"images":{"16":{"digest":"` + ovDigest + `"}}}}}`
	signImages(t, w, validIdentity, pgRef16, pgRef17) // i default sono firmati, l'override no
	runRefused(t, w, "immagine postgres", ovRef16, "major 16")
	if w.helm.upgradeValues != "" {
		t.Errorf("helm upgrade lanciato con un digest effettivo non firmato: %s", w.helm.upgradeValues)
	}
	if calledWith(t, pgRef16) {
		t.Error("il digest di default è sostituito dall'override: non va verificato al suo posto")
	}
}

func TestDatabaseDigests_OverrideConservatoFirmatoProcede(t *testing.T) {
	w := dbWorld(t)
	w.helm.values = `{"databases":{"enabled":true,"postgres":{"images":{"16":{"digest":"` + ovDigest + `"}}}}}`
	signImages(t, w, validIdentity, ovRef16, pgRef17)
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if !calledWith(t, ovRef16) {
		t.Errorf("il riferimento effettivo va verificato: %v", cosignCalls(t))
	}
}

// -f e --set hanno la precedenza di Helm: --set vince su -f, che vince sui valori conservati.
func TestDatabaseDigests_FileEDSetConPrecedenzaDiHelm(t *testing.T) {
	w := dbWorld(t)
	setDigest := "sha256:" + strings.Repeat("c", 64)
	f := filepath.Join(t.TempDir(), "extra.yaml")
	mustWrite(t, f, "databases:\n  postgres:\n    images:\n      \"17\":\n        digest: "+ovDigest+"\n")
	w.o.ValueFiles = []string{f}
	w.o.Sets = []string{"databases.postgres.images.17.digest=" + setDigest}
	signImages(t, w, validIdentity, pgRef16)
	// Né quello del file né il default della 17 sono firmati; quello di --set (l'effettivo) nemmeno.
	runRefused(t, w, "immagine postgres", "registry.test/postgres@"+setDigest, "major 17")
	if w.helm.upgradeValues != "" {
		t.Error("helm upgrade lanciato")
	}
	// Firmato quello di --set: procede, e gli altri due non servono.
	w2 := dbWorld(t)
	w2.o.ValueFiles = []string{f}
	w2.o.Sets = []string{"databases.postgres.images.17.digest=" + setDigest}
	signImages(t, w2, validIdentity, pgRef16, "registry.test/postgres@"+setDigest)
	res, err := Run(context.Background(), w2.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if calledWith(t, ovDigest) || calledWith(t, pgRef17) {
		t.Errorf("valori scavalcati verificati: %v", cosignCalls(t))
	}
}

// Stesso digest ma repository diverso (override con --set): è un riferimento nuovo, va verificato.
func TestDatabaseDigests_StessoDigestRepositoryDiverso(t *testing.T) {
	w := dbWorld(t)
	mustWrite(t, filepath.Join(w.chart, "values.yaml"), dbValues(pgDigest16, pgDigest17))
	w.o.Sets = []string{"databases.postgres.images.16.repository=altro.test/pg"}
	signImages(t, w, validIdentity, pgRef17)
	_, err := Run(context.Background(), w.o)
	if err == nil || !strings.Contains(err.Error(), "altro.test/pg@"+pgDigest16) {
		t.Fatalf("atteso rifiuto sul riferimento altro.test/pg@%s, errore: %v", pgDigest16, err)
	}
	if w.helm.upgradeValues != "" {
		t.Error("helm upgrade lanciato")
	}
}

// Riferimento effettivo invariato (stesso override già conservato e già nella release): riuso, niente cosign.
func TestDatabaseDigests_RiferimentoInvariatoRiuso(t *testing.T) {
	w := dbWorld(t)
	over := `{"databases":{"enabled":true,"postgres":{"images":{"16":{"digest":"` + ovDigest + `"}}}}}`
	w.helm.values = over
	// La copia attuale del chart ha un default diverso, ma l'effettivo di prima era già l'override.
	mustWrite(t, filepath.Join(w.chart, "values.yaml"), dbValues("sha256:"+strings.Repeat("d", 64), pgDigest17))
	signImages(t, w, validIdentity) // nessuna firma: tutto deve essere riusato
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if calledWith(t, "registry.test/postgres") {
		t.Errorf("riferimenti effettivi invariati: non si riverificano: %v", cosignCalls(t))
	}
}

func TestSetValueEMergeCoerentiConHelm(t *testing.T) {
	v := map[string]any{"a": map[string]any{"b": "1", "c": "2"}}
	v = mergeValues(v, map[string]any{"a": map[string]any{"b": "9", "c": nil}, "d": true})
	if a := v["a"].(map[string]any); a["b"] != "9" || len(a) != 1 || v["d"] != true {
		t.Errorf("merge: %v", v)
	}
	setValue(v, `x.y\.z.16.digest=sha256:aa`)
	setValue(v, "databases.enabled=true")
	if got := v["x"].(map[string]any)["y.z"].(map[string]any)["16"].(map[string]any)["digest"]; got != "sha256:aa" {
		t.Errorf("set annidato/escape: %v", v)
	}
	if v["databases"].(map[string]any)["enabled"] != true {
		t.Errorf("bool: %v", v)
	}
}
