//go:build integration

package httpserver_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
)

// GIT-349 (v3-a, H7/H10): firme al deploy, chiavi fidate, «solo immagini firmate» e deploy
// automatico da branch. Il servizio git è finto: dice chi ha firmato ogni tag.

const (
	sigGitSecret = "segreto-del-servizio-git"
	sigDigestA   = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	sigDigestB   = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	sigDigestC   = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	sigDigestD   = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	sigDigestE   = "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	sigCommit    = "0123456789abcdef0123456789abcdef01234567"
)

// sigTag descrive un tag del registry finto: il digest, chi lo ha firmato e il commit.
type sigTag struct {
	digest string
	// signer: "" (nessuna firma), "instance" (CI del repo), "foreign" (firma della CI di un altro
	// repo: git la scarta), "trusted" (la chiave in trustedPEM).
	signer     string
	trustedPEM string
	revision   string
}

type sigGit struct {
	*commitGitPkg
	smu    sync.Mutex
	tags   map[string]sigTag // per tag
	verify []string          // tag verificati, in ordine
	pem    string
}

func newSigGit(t *testing.T) *sigGit {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &sigGit{commitGitPkg: &commitGitPkg{fakeGit: newFakeGit()}, tags: map[string]sigTag{}, pem: testPublicKeyPEM(t, k)}
}

func testPublicKeyPEM(t *testing.T, k *ecdsa.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func (g *sigGit) find(tag, digest string) (string, sigTag, bool) {
	for name, st := range g.tags {
		if (digest != "" && st.digest == digest) || (digest == "" && name == tag) {
			return name, st, true
		}
	}
	return "", sigTag{}, false
}

func (g *sigGit) ImageRevision(_ context.Context, _ trust.Identity, _ uuid.UUID, _, tag, digest string) (string, error) {
	g.smu.Lock()
	defer g.smu.Unlock()
	if _, st, ok := g.find(tag, digest); ok {
		return st.revision, nil
	}
	return "", gitclient.ErrNotFound
}

func (g *sigGit) VerifyImage(_ context.Context, _ trust.Identity, _ uuid.UUID, _, tag, digest, repoRef string, trustedKeys []string) (gitclient.SignatureCheck, error) {
	g.smu.Lock()
	defer g.smu.Unlock()
	name, st, ok := g.find(tag, digest)
	if !ok {
		return gitclient.SignatureCheck{}, gitclient.ErrNotFound
	}
	g.verify = append(g.verify, name)
	out := gitclient.SignatureCheck{Digest: st.digest}
	switch st.signer {
	case "instance":
		if repoRef == "alice/handbook" {
			out.Signer = gitclient.SignerInstance
		}
	case "trusted":
		if i := slices.IndexFunc(trustedKeys, func(k string) bool { return strings.TrimSpace(k) == strings.TrimSpace(st.trustedPEM) }); i >= 0 {
			out.Signer, out.KeyIndex = gitclient.SignerTrusted, i
		}
	}
	return out, nil
}

func (g *sigGit) InstanceSigningKey(context.Context, trust.Identity) (string, error) {
	return g.pem, nil
}

type sigEnv struct {
	*appsEnv
	g      *sigGit
	router http.Handler
	repoID uuid.UUID
}

func newSigEnv(t *testing.T) *sigEnv {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	id := &issuesIdentity{fakeIdentity: newFakeIdentity(), writers: map[uuid.UUID][]string{}}
	id.sysAdmin[users["carol"]] = true
	e := &issuesEnv{t: t, pool: pool, id: id}
	g := newSigGit(t)
	clock := &appClock{t: time.Now().UTC().Truncate(time.Second)}
	e.router = httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret,
		httpserver.WithRepoIdentity(id), httpserver.WithReadableLister(id), httpserver.WithGit(g),
		httpserver.WithCloneConfig(httpserver.CloneConfig{PublicURL: "https://git.example.com"}),
		httpserver.WithAppsDomain("apps.example.com"), httpserver.WithClock(clock.Now),
		httpserver.WithAppController(commitNoopController{}), httpserver.WithGitSecret(sigGitSecret))
	se := &sigEnv{appsEnv: &appsEnv{issuesEnv: e, clock: clock, git: g.fakeGit}, g: g, router: e.router}
	se.repoID = e.repo("handbook", true)
	return se
}

// pushed simula la notifica del servizio git per un tag pubblicato da un job.
func (e *sigEnv) pushed(secret, tag, digest, ref, event, actor string) *httptest.ResponseRecorder {
	e.t.Helper()
	body, _ := json.Marshal(map[string]string{"image": "web", "tag": tag, "digest": digest, "runId": uuid.NewString(), "ref": ref, "event": event, "actorId": actor})
	req := httptest.NewRequest(http.MethodPost, "/internal/registry/image-pushed/"+e.repoID.String(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	trust.Sign(req.Header, secret, trust.Identity{UserID: aliceID, Username: "gitstack-registry"}, e.clock.Now())
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e *sigEnv) versionCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.app_versions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *sigEnv) trustedKey(rec *httptest.ResponseRecorder) openapi.AppTrustedKey {
	e.t.Helper()
	var k openapi.AppTrustedKey
	if err := json.Unmarshal(rec.Body.Bytes(), &k); err != nil {
		e.t.Fatalf("chiave non valida: %v: %s", err, rec.Body.String())
	}
	return k
}

const settingsBody = `{"publicAppsEnabled":false,"signedImagesOnly":%s,"restricted":false,"maxPerApp":{"cpuMillis":4000,"memoryBytes":8589934592},"logRetention":{"days":7,"maxBytes":1073741824}}`

func (e *sigEnv) signedOnly(on string) {
	e.t.Helper()
	body := strings.Replace(settingsBody, "%s", on, 1)
	rec := e.do(http.MethodPut, "/admin/apps/settings", "carol", body)
	e.want(rec, http.StatusOK, "")
	var s openapi.AppsInstanceSettings
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil || s.SignedImagesOnly != (on == "true") {
		e.t.Fatalf("impostazioni: %v %s", err, rec.Body.String())
	}
}

// Il deploy a mano accetta anche immagini non firmate, etichettate «non verificata»; con «solo
// immagini firmate» le rifiuta, e una chiave fidata aggiuntiva le fa passare.
func TestSignatures_DeployManualeSoloFirmateEChiaveFidata(t *testing.T) {
	e := newSigEnv(t)
	trustedPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	trustedPEM := testPublicKeyPEM(t, trustedPriv)
	e.g.tags = map[string]sigTag{
		"ci":   {digest: sigDigestA, signer: "instance"},
		"hand": {digest: sigDigestB},
		"ext":  {digest: sigDigestC, signer: "trusted", trustedPEM: trustedPEM},
		"copy": {digest: sigDigestD, signer: "foreign"},
	}
	base := "/repos/alice/handbook/apps"
	img := func(tag string) string { return `{"image":"git.example.com/alice/handbook/web:` + tag + `"}` }

	// Creazione con un'immagine firmata dalla CI: verificata.
	rec := e.do(http.MethodPost, base, "alice", `{"name":"web","image":"git.example.com/alice/handbook/web:ci","port":8080}`)
	e.want(rec, http.StatusAccepted, "")
	var created openapi.AppCreated
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if !created.Version.Image.Verified || created.Version.Image.SignedBy == nil || *created.Version.Image.SignedBy != "alice/handbook CI" {
		t.Fatalf("immagine firmata dalla CI: %+v", created.Version.Image)
	}
	e.finishCommitDeploy(t, "web")

	// A mano, non firmata o con la firma di un altro repo: passa, «non verificata».
	for i, tag := range []string{"hand", "copy"} {
		rec = e.do(http.MethodPost, base+"/web/deploys", "alice", img(tag))
		e.want(rec, http.StatusAccepted, "")
		var v openapi.AppVersion
		_ = json.Unmarshal(rec.Body.Bytes(), &v)
		if v.Image.Verified || v.Image.SignedBy != nil {
			t.Fatalf("%s deve essere «non verificata»: %+v", tag, v.Image)
		}
		if got := e.versionOf(base+"/web/versions/"+itoa(v.Number), "alice"); got.Image.Verified {
			t.Fatalf("la versione %d (%d) riletta: %+v", v.Number, i, got.Image)
		}
		e.finishCommitDeploy(t, "web")
	}
	// Un'immagine esterna non si può verificare: «non verificata».
	rec = e.do(http.MethodPost, base+"/web/deploys", "alice", `{"image":"ghcr.io/alice/web:v1"}`)
	e.want(rec, http.StatusAccepted, "")
	e.finishCommitDeploy(t, "web")

	// Con «solo immagini firmate» (solo l'amministratore lo imposta) tutto ciò che non è firmato è rifiutato.
	e.want(e.do(http.MethodPut, "/admin/apps/settings", "alice", strings.Replace(settingsBody, "%s", "true", 1)), http.StatusForbidden, "")
	e.signedOnly("true")
	before := e.versionCount(t)
	for _, body := range []string{img("hand"), img("copy"), img("ext"), `{"image":"ghcr.io/alice/web:v1"}`} {
		e.want(e.do(http.MethodPost, base+"/web/deploys", "alice", body), http.StatusUnprocessableEntity, "image_not_allowed")
	}
	e.want(e.do(http.MethodPost, base, "alice", `{"name":"api","image":"git.example.com/alice/handbook/web:hand"}`), http.StatusUnprocessableEntity, "image_not_allowed")
	if e.versionCount(t) != before {
		t.Fatal("un'immagine rifiutata non deve lasciare versioni")
	}
	// La firma della CI vale ancora.
	e.want(e.do(http.MethodPost, base+"/web/deploys", "alice", img("ci")), http.StatusAccepted, "")
	e.finishCommitDeploy(t, "web")

	// Chiavi fidate: la chiave dell'istanza c'è sempre e non si toglie; le altre le gestisce l'amministratore.
	e.want(e.do(http.MethodGet, "/admin/apps/trusted-keys", "alice", ""), http.StatusForbidden, "")
	rec = e.do(http.MethodGet, "/admin/apps/trusted-keys", "carol", "")
	e.want(rec, http.StatusOK, "")
	var list openapi.AppTrustedKeyList
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Items) != 1 || !list.Items[0].InstanceKey || list.Items[0].PublicKey == nil || *list.Items[0].PublicKey != e.g.pem {
		t.Fatalf("elenco iniziale: %s", rec.Body.String())
	}
	instID := list.Items[0].Id
	keyBody, _ := json.Marshal(map[string]string{"name": "CI esterna", "publicKey": trustedPEM})
	e.want(e.do(http.MethodPost, "/admin/apps/trusted-keys", "alice", string(keyBody)), http.StatusForbidden, "")
	e.want(e.do(http.MethodPost, "/admin/apps/trusted-keys", "carol", `{"name":"x","publicKey":"non è una chiave"}`), http.StatusUnprocessableEntity, "invalid_key")
	e.want(e.do(http.MethodPost, "/admin/apps/trusted-keys", "carol", `{"name":"x","publicKey":"`+strings.ReplaceAll(e.g.pem, "\n", `\n`)+`"}`), http.StatusConflict, "key_exists")
	rec = e.do(http.MethodPost, "/admin/apps/trusted-keys", "carol", string(keyBody))
	e.want(rec, http.StatusCreated, "")
	key := e.trustedKey(rec)
	if key.InstanceKey || key.Name != "CI esterna" || len(key.Fingerprint) != 64 || key.CreatedBy == nil || key.CreatedBy.Username != "carol" {
		t.Fatalf("chiave aggiunta: %+v", key)
	}
	e.want(e.do(http.MethodPost, "/admin/apps/trusted-keys", "carol", string(keyBody)), http.StatusConflict, "key_exists")

	// Con la chiave fidata il deploy a mano passa e ricorda chi ha firmato; l'immagine non firmata no.
	rec = e.do(http.MethodPost, base+"/web/deploys", "alice", img("ext"))
	e.want(rec, http.StatusAccepted, "")
	var v openapi.AppVersion
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	if !v.Image.Verified || v.Image.SignedBy == nil || *v.Image.SignedBy != "CI esterna" {
		t.Fatalf("firmata dalla chiave fidata: %+v", v.Image)
	}
	e.finishCommitDeploy(t, "web")
	e.want(e.do(http.MethodPost, base+"/web/deploys", "alice", img("hand")), http.StatusUnprocessableEntity, "image_not_allowed")

	// Il ritorno a una versione con firma valida passa; togliendo la chiave, il deploy e il ritorno la rifiutano.
	e.want(e.do(http.MethodDelete, "/admin/apps/trusted-keys/"+uuid.UUID(instID).String(), "carol", ""), http.StatusConflict, "instance_key")
	e.want(e.do(http.MethodDelete, "/admin/apps/trusted-keys/"+uuid.NewString(), "carol", ""), http.StatusNotFound, "")
	e.want(e.do(http.MethodDelete, "/admin/apps/trusted-keys/"+uuid.UUID(key.Id).String(), "alice", ""), http.StatusForbidden, "")
	e.want(e.do(http.MethodDelete, "/admin/apps/trusted-keys/"+uuid.UUID(key.Id).String(), "carol", ""), http.StatusNoContent, "")
	e.want(e.do(http.MethodPost, base+"/web/deploys", "alice", img("ext")), http.StatusUnprocessableEntity, "image_not_allowed")
	e.want(e.do(http.MethodPost, base+"/web/rollback", "alice", `{"version":`+itoa(v.Number)+`}`), http.StatusUnprocessableEntity, "image_not_allowed")

	// Spenta la regola, torna tutto accettato.
	e.signedOnly("false")
	e.want(e.do(http.MethodPost, base+"/web/deploys", "alice", img("hand")), http.StatusAccepted, "")
}

// Seguire un branch vuole un'immagine firmata dalla CI di questo repo (H7, H10): non una non firmata,
// non quella di un altro repo, non quella di una chiave fidata aggiuntiva.
func TestSignatures_SeguireUnBranchRichiedeLaFirmaDellaCI(t *testing.T) {
	e := newSigEnv(t)
	trustedPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	trustedPEM := testPublicKeyPEM(t, trustedPriv)
	e.g.tags = map[string]sigTag{
		"ci":   {digest: sigDigestA, signer: "instance"},
		"hand": {digest: sigDigestB},
		"ext":  {digest: sigDigestC, signer: "trusted", trustedPEM: trustedPEM},
		"copy": {digest: sigDigestD, signer: "foreign"},
	}
	keyBody, _ := json.Marshal(map[string]string{"name": "CI esterna", "publicKey": trustedPEM})
	e.want(e.do(http.MethodPost, "/admin/apps/trusted-keys", "carol", string(keyBody)), http.StatusCreated, "")
	base := "/repos/alice/handbook/apps"
	mk := func(name, tag string) string {
		return `{"name":"` + name + `","image":"git.example.com/alice/handbook/web:` + tag + `","port":8080,"followBranch":"main"}`
	}
	for _, tag := range []string{"hand", "ext", "copy"} {
		e.want(e.do(http.MethodPost, base, "alice", mk("w-"+tag, tag)), http.StatusUnprocessableEntity, "follow_branch_unsigned")
	}
	e.want(e.do(http.MethodPost, base, "alice", mk("w-ci", "ci")), http.StatusAccepted, "")
	e.want(e.do(http.MethodPost, base, "alice", strings.Replace(mk("w-ext", "ext"), `,"followBranch":"main"`, "", 1)), http.StatusAccepted, "")
	e.want(e.do(http.MethodPost, base, "alice", strings.Replace(mk("w-hand", "hand"), `,"followBranch":"main"`, "", 1)), http.StatusAccepted, "")
	// Un'app con un'immagine non firmata non può mettersi a seguire un branch.
	e.want(e.do(http.MethodPatch, base+"/w-hand", "alice", `{"followBranch":"main"}`), http.StatusUnprocessableEntity, "follow_branch_unsigned")
	e.want(e.do(http.MethodPatch, base+"/w-ext", "alice", `{"followBranch":"main"}`), http.StatusUnprocessableEntity, "follow_branch_unsigned")
	e.finishCommitDeploy(t, "w-ci")
	e.want(e.do(http.MethodPatch, base+"/w-ci", "alice", `{"followBranch":"develop"}`), http.StatusOK, "")
}

// Con un'immagine firmata dalla CI si può seguire un branch insieme a autoDeployPaused e poi toglierlo
// con followBranch:null: nessuna delle due modifiche crea una versione (H8).
func TestSignatures_FollowBranchEPausaNonCreanoVersioni(t *testing.T) {
	e := newSigEnv(t)
	e.g.tags = map[string]sigTag{"ci": {digest: sigDigestA, signer: "instance"}}
	base := "/repos/alice/handbook/apps"
	e.want(e.do(http.MethodPost, base, "alice", `{"name":"w-ci","image":"git.example.com/alice/handbook/web:ci","port":8080}`), http.StatusAccepted, "")
	e.finishCommitDeploy(t, "w-ci")
	n := e.versionCount(t)

	a := e.app(e.do(http.MethodPatch, base+"/w-ci", "alice", `{"followBranch":"main","autoDeployPaused":true}`))
	if a.Follow == nil || a.Follow.Branch == nil || *a.Follow.Branch != "main" || !a.Follow.Paused {
		t.Fatalf("follow: %+v", a.Follow)
	}
	a = e.app(e.do(http.MethodPatch, base+"/w-ci", "alice", `{"followBranch":null}`))
	if a.Follow != nil {
		t.Fatalf("followBranch null non toglie il branch: %+v", a.Follow)
	}
	if got := e.versionCount(t); got != n {
		t.Fatalf("versioni: %d, attese %d", got, n)
	}
}

// Il deploy automatico: push → immagine firmata dalla CI → nuova versione con i permessi di chi ha
// fatto il push, trigger branch_push, commit dell'immagine (H14). Mai per immagini non firmate o di
// altri repo, né per chi non ha più `write`.
func TestAutoDeploy_DaBranchSoloImmaginiFirmateDallaCI(t *testing.T) {
	e := newSigEnv(t)
	trustedPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	trustedPEM := testPublicKeyPEM(t, trustedPriv)
	e.g.tags = map[string]sigTag{
		"sha-1":  {digest: sigDigestA, signer: "instance", revision: sigCommit},
		"sha-2":  {digest: sigDigestB, signer: "instance", revision: "fedcba9876543210fedcba9876543210fedcba98"},
		"hand":   {digest: sigDigestC},
		"ext":    {digest: sigDigestD, signer: "trusted", trustedPEM: trustedPEM},
		"copy":   {digest: sigDigestE, signer: "foreign"},
		"latest": {digest: sigDigestB, signer: "instance", revision: "fedcba9876543210fedcba9876543210fedcba98"}, // stesso build di sha-2
	}
	keyBody, _ := json.Marshal(map[string]string{"name": "CI esterna", "publicKey": trustedPEM})
	e.want(e.do(http.MethodPost, "/admin/apps/trusted-keys", "carol", string(keyBody)), http.StatusCreated, "")
	e.id.grantWrite(e.repoID, "bob")
	delete(e.id.sysAdmin, users["carol"]) // da qui carol è un utente qualunque: legge il repo, non lo scrive
	base := "/repos/alice/handbook/apps"
	e.want(e.do(http.MethodPost, base, "alice", `{"name":"web","image":"git.example.com/alice/handbook/web:sha-1","port":8080,"followBranch":"main"}`), http.StatusAccepted, "")
	e.want(e.do(http.MethodPost, base, "alice", `{"name":"fixed","image":"git.example.com/alice/handbook/web:sha-1","port":8080}`), http.StatusAccepted, "")
	e.finishCommitDeploy(t, "web")
	e.finishCommitDeploy(t, "fixed")
	bob, carol := users["bob"], users["carol"]

	// Il servizio git firma la notifica con la sua chiave: un'altra no.
	e.want(e.pushed("segreto-sbagliato", "sha-2", sigDigestB, "refs/heads/main", "push", bob), http.StatusUnauthorized, "")
	e.want(e.pushed(sigGitSecret, "sha-2", "non-un-digest", "refs/heads/main", "push", bob), http.StatusBadRequest, "")
	n := e.versionCount(t)
	noDeploy := func(why, tag, digest, ref, event, actor string) {
		t.Helper()
		e.want(e.pushed(sigGitSecret, tag, digest, ref, event, actor), http.StatusNoContent, "")
		if got := e.versionCount(t); got != n {
			t.Fatalf("%s: nessuna versione nuova attesa, ce ne sono %d in più", why, got-n)
		}
	}
	noDeploy("non firmata", "hand", sigDigestC, "refs/heads/main", "push", bob)
	noDeploy("firmata da un altro repo", "copy", sigDigestE, "refs/heads/main", "push", bob)
	noDeploy("chiave fidata aggiuntiva: vale per il deploy a mano, non per il automatico", "ext", sigDigestD, "refs/heads/main", "push", bob)
	noDeploy("un altro branch", "sha-2", sigDigestB, "refs/heads/develop", "push", bob)
	noDeploy("un tag git", "sha-2", sigDigestB, "refs/tags/v1", "push", bob)
	noDeploy("una pull request", "sha-2", sigDigestB, "refs/heads/main", "pull_request", bob)
	noDeploy("chi ha fatto il push non ha `write`", "sha-2", sigDigestB, "refs/heads/main", "push", carol)
	noDeploy("attore sconosciuto", "sha-2", sigDigestB, "refs/heads/main", "push", "non-un-uuid")

	// Il deploy automatico: nuova versione, trigger branch_push, branch, autore e commit dell'immagine.
	e.want(e.pushed(sigGitSecret, "sha-2", sigDigestB, "refs/heads/main", "push", bob), http.StatusNoContent, "")
	if got := e.versionCount(t); got != n+1 {
		t.Fatalf("versioni dopo il deploy automatico: %d, attese %d", got, n+1)
	}
	v := e.versionOf(base+"/web/versions/2", "alice")
	if v.Trigger != openapi.AppVersionTrigger("branch_push") || v.Branch == nil || *v.Branch != "main" || !v.Image.Verified ||
		v.Image.SignedBy == nil || *v.Image.SignedBy != "alice/handbook CI" || v.Image.Digest == nil || *v.Image.Digest != sigDigestB ||
		!strings.HasSuffix(v.Image.Reference, "/web:sha-2") || v.CreatedBy == nil || v.CreatedBy.Username != "bob" || v.State != openapi.AppDeployState("in_progress") {
		t.Fatalf("versione del deploy automatico: %+v", v)
	}
	// H14: commit_sha dall'etichetta revision dell'immagine, quindi GITSTACK_COMMIT_SHA nel pod.
	if got := e.commitInDB(t, "web", 2); got == nil || *got != "fedcba9876543210fedcba9876543210fedcba98" {
		t.Fatalf("commit_sha del deploy automatico = %v", got)
	}
	// L'app che non segue il branch non si tocca.
	if got := e.versionOf(base+"/fixed/versions/1", "alice"); got.Number != 1 {
		t.Fatal("l'app fissa non doveva cambiare")
	}
	var fixedVersions int
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.app_versions v JOIN core.apps a ON a.id = v.app_id WHERE a.name = 'fixed'`).Scan(&fixedVersions)
	if fixedVersions != 1 {
		t.Fatalf("l'app fissa ha %d versioni", fixedVersions)
	}
	n = e.versionCount(t)
	// Con il deploy ancora in corso un secondo tag non ne crea un altro.
	noDeploy("deploy già in corso", "sha-1", sigDigestA, "refs/heads/main", "push", bob)
	e.finishCommitDeploy(t, "web")
	e.sql(`UPDATE core.apps SET status = 'running'`)
	// Lo stesso build con un altro tag (stesso digest): niente di nuovo.
	noDeploy("stesso digest", "latest", sigDigestB, "refs/heads/main", "push", bob)
	// Sospeso (per esempio dopo un rollback): nessun deploy automatico.
	e.want(e.do(http.MethodPatch, base+"/web", "alice", `{"autoDeployPaused":true}`), http.StatusOK, "")
	n = e.versionCount(t)
	noDeploy("deploy automatico sospeso", "sha-1", sigDigestA, "refs/heads/main", "push", bob)
	e.want(e.do(http.MethodPatch, base+"/web", "alice", `{"autoDeployPaused":false}`), http.StatusOK, "")
	n = e.versionCount(t)
	// Dopo la ripresa un build nuovo sul branch scatta, anche da workflow_dispatch.
	e.g.tags["sha-3"] = sigTag{digest: sigDigestA, signer: "instance"}
	delete(e.g.tags, "sha-1")
	e.want(e.pushed(sigGitSecret, "sha-3", sigDigestA, "refs/heads/main", "workflow_dispatch", bob), http.StatusNoContent, "")
	if got := e.versionCount(t); got != n+1 {
		t.Fatalf("workflow_dispatch: versioni %d, attese %d", got, n+1)
	}
	// A mano resta possibile e la storia mostra anche l'origine dei deploy.
	l := e.versions(base+"/web/versions", "alice")
	var triggers []string
	for _, it := range l.Items {
		triggers = append(triggers, string(it.Trigger))
	}
	if !slices.Contains(triggers, "branch_push") {
		t.Fatalf("storia delle versioni: %v", triggers)
	}
}

// Un repo archiviato o un'app senza `write` per chi ha fatto il push: nessun deploy, nessun errore.
func TestAutoDeploy_RepoArchiviatoENotificheMalformate(t *testing.T) {
	e := newSigEnv(t)
	e.g.tags = map[string]sigTag{"sha-1": {digest: sigDigestA, signer: "instance"}, "sha-2": {digest: sigDigestB, signer: "instance"}}
	base := "/repos/alice/handbook/apps"
	e.want(e.do(http.MethodPost, base, "alice", `{"name":"web","image":"git.example.com/alice/handbook/web:sha-1","port":8080,"followBranch":"main"}`), http.StatusAccepted, "")
	e.finishCommitDeploy(t, "web")
	e.sql(`UPDATE core.repositories SET archived_at = now() WHERE resource_id = $1`, e.repoID)
	n := e.versionCount(t)
	e.want(e.pushed(sigGitSecret, "sha-2", sigDigestB, "refs/heads/main", "push", users["alice"]), http.StatusNoContent, "")
	if e.versionCount(t) != n {
		t.Fatal("un repo archiviato non fa deploy")
	}
	// L'id del repo non valido o sconosciuto: 404.
	req := httptest.NewRequest(http.MethodPost, "/internal/registry/image-pushed/"+uuid.NewString(), strings.NewReader(`{"tag":"x","digest":"`+sigDigestA+`"}`))
	trust.Sign(req.Header, sigGitSecret, trust.Identity{UserID: aliceID, Username: "gitstack-registry"}, e.clock.Now())
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("repo sconosciuto: %d", rec.Code)
	}
}

// jobToken crea un run (con l'attore indicato, o nessuno) e un job in corso con i permessi dati e il token.
func (e *sigEnv) jobToken(token, permissions string, actor *string) {
	e.t.Helper()
	ctx := context.Background()
	run, job := uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO core.ci_runs (id, repo_id, owner_type, owner_id, workflow_path, event, ref, sha, group_key, status, actor_id)
			VALUES ($1, $2, 'user', $3, '.github/workflows/ci.yml', 'push', 'refs/heads/main', repeat('a', 40), $4, 'in_progress', $5)`, []any{run, e.repoID, uuid.New(), "g-" + run.String(), actor}},
		{`INSERT INTO core.ci_jobs (id, run_id, seq, job_key, name, status, permissions) VALUES ($1, $2, 0, 'deploy', 'deploy', 'in_progress', $3::jsonb)`, []any{job, run, permissions}},
		{`INSERT INTO core.ci_job_tokens (job_id, token_hash, issued_at, expires_at) VALUES ($1, $2, now(), now() + interval '1 hour')`, []any{job, hash(token)}},
	} {
		if _, err := e.pool.Exec(ctx, q.sql, q.args...); err != nil {
			e.t.Fatalf("%s: %v", q.sql, err)
		}
	}
}

func (e *sigEnv) asJob(path, token, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	trust.Sign(req.Header, trustSecret, trust.Identity{UserID: uuid.Nil.String(), Username: trust.JobUsername, Scopes: []string{trust.JobScope}}, e.clock.Now())
	if token != "" {
		req.Header.Set(trust.HeaderJobToken, token)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

// Un workflow con `packages: write` fa il deploy con il token del job, a nome di chi ha causato il
// run (CI4); l'immagine non firmata passa come «non verificata» e, con «solo firmate», no (H7, H10).
func TestApps_DeployConIlTokenDelJob(t *testing.T) {
	e := newSigEnv(t)
	e.g.tags = map[string]sigTag{"ci": {digest: sigDigestA, signer: "instance"}, "hand": {digest: sigDigestB}}
	e.id.grantWrite(e.repoID, "bob")
	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", `{"name":"web","image":"git.example.com/alice/handbook/web:ci","port":8080}`), http.StatusAccepted, "")
	e.finishCommitDeploy(t, "web")
	bob, nobody := users["bob"], uuid.NewString()
	e.jobToken("gsj_pubblica", `{"contents":"read","packages":"write","actions":"read"}`, &bob)
	e.jobToken("gsj_legge", `{"contents":"write","packages":"read","actions":"read"}`, &bob)
	e.jobToken("gsj_senza_autore", `{"contents":"read","packages":"write","actions":"read"}`, nil)
	e.jobToken("gsj_senza_write", `{"contents":"read","packages":"write","actions":"read"}`, &nobody)
	path := "/repos/alice/handbook/apps/web/deploys"
	body := `{"image":"git.example.com/alice/handbook/web:hand"}`

	rec := e.asJob(path, "gsj_pubblica", body)
	e.want(rec, http.StatusAccepted, "")
	var v openapi.AppVersion
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	if v.Trigger != openapi.AppVersionTrigger("manual") || v.Image.Verified || v.CreatedBy == nil || v.CreatedBy.Username != "bob" {
		t.Fatalf("deploy del job: %+v", v)
	}
	e.finishCommitDeploy(t, "web")
	// packages: read non basta; né un run senza autore, né un autore che non ha write.
	e.want(e.asJob(path, "gsj_legge", body), http.StatusForbidden, "")
	e.want(e.asJob(path, "gsj_senza_autore", body), http.StatusForbidden, "")
	e.want(e.asJob(path, "gsj_senza_write", body), http.StatusForbidden, "")
	e.want(e.asJob(path, "", body), http.StatusUnauthorized, "")
	e.want(e.asJob(path, "gsj_inventato", body), http.StatusUnauthorized, "")
	// Solo il deploy: nessun'altra operazione delle app con il token del job.
	e.want(e.asJob("/repos/alice/handbook/apps/web/rollback", "gsj_pubblica", `{"version":1}`), http.StatusForbidden, "")
	e.want(e.asJob("/repos/alice/handbook/apps", "gsj_pubblica", `{"name":"x","image":"ci"}`), http.StatusForbidden, "")
	// Con «solo immagini firmate» il job non aggira la regola.
	e.signedOnly("true")
	e.want(e.asJob(path, "gsj_pubblica", body), http.StatusUnprocessableEntity, "image_not_allowed")
	e.want(e.asJob(path, "gsj_pubblica", `{"image":"git.example.com/alice/handbook/web:ci"}`), http.StatusAccepted, "")
}
