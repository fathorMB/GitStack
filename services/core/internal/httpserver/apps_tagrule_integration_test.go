//go:build integration

package httpserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// GIT-377 (v3-a, H7 del 2026-10-26): deploy automatico da regola sui tag e deploy di un tag preciso
// dalla CI. Il servizio git è finto (sigGit): dice chi ha firmato ogni tag.

const (
	tagCommitA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	tagCommitB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestAutoDeploy_DaRegolaSuiTag(t *testing.T) {
	e := newSigEnv(t)
	e.g.tags = map[string]sigTag{
		"ci":          {digest: sigDigestA, signer: "instance", revision: sigCommit},
		"v1.2-test.3": {digest: sigDigestB, signer: "instance", revision: tagCommitA},
		"v1.2":        {digest: sigDigestC, signer: "instance", revision: tagCommitB},
		"v1.3-test.1": {digest: sigDigestD}, // non firmato
		"v1.4-test.1": {digest: sigDigestE, signer: "foreign"},
	}
	e.id.grantWrite(e.repoID, "bob")
	base := "/repos/alice/handbook/apps"
	img := "git.example.com/alice/handbook/web:ci"

	// Branch e regola sui tag insieme: rifiutati con un messaggio chiaro, alla creazione e alla modifica.
	rec := e.do(http.MethodPost, base, "alice", `{"name":"both","image":"`+img+`","port":8080,"followBranch":"main","followTagPattern":"v*-test.*"}`)
	e.want(rec, http.StatusUnprocessableEntity, "follow_both")
	if !strings.Contains(rec.Body.String(), "un branch oppure una regola sui tag") {
		t.Fatalf("messaggio poco chiaro: %s", rec.Body.String())
	}
	e.want(e.do(http.MethodPost, base, "alice", `{"name":"bad","image":"`+img+`","followTagPattern":"v[*"}`), http.StatusUnprocessableEntity, "")
	e.want(e.do(http.MethodPost, base, "alice", `{"name":"web","image":"`+img+`","port":8080,"followTagPattern":"v*-test.*"}`), http.StatusAccepted, "")
	e.finishCommitDeploy(t, "web")
	var app openapi.App
	if err := json.Unmarshal(e.do(http.MethodGet, base+"/web", "alice", "").Body.Bytes(), &app); err != nil ||
		app.Follow == nil || app.Follow.TagPattern == nil || *app.Follow.TagPattern != "v*-test.*" || app.Follow.Branch != nil {
		t.Fatalf("follow dell'app: %v %+v", err, app.Follow)
	}
	rec = e.do(http.MethodPatch, base+"/web", "alice", `{"followBranch":"main"}`)
	e.want(rec, http.StatusUnprocessableEntity, "follow_both")
	// Nella stessa richiesta si può passare da una sorgente all'altra e tornare indietro.
	e.want(e.do(http.MethodPatch, base+"/web", "alice", `{"followBranch":"main","followTagPattern":null}`), http.StatusOK, "")
	e.want(e.do(http.MethodPatch, base+"/web", "alice", `{"followBranch":"main","followTagPattern":"v*"}`), http.StatusUnprocessableEntity, "follow_both")
	e.want(e.do(http.MethodPatch, base+"/web", "alice", `{"followBranch":null,"followTagPattern":"v*-test.*"}`), http.StatusOK, "")

	delete(e.id.sysAdmin, users["carol"]) // da qui carol legge il repo ma non lo scrive
	bob := users["bob"]
	n := e.versionCount(t)
	noDeploy := func(why, tag, digest, event, actor string) {
		t.Helper()
		e.want(e.pushed(sigGitSecret, tag, digest, "refs/heads/main", event, actor), http.StatusNoContent, "")
		if got := e.versionCount(t); got != n {
			t.Fatalf("%s: nessuna versione nuova attesa, ce ne sono %d in più", why, got-n)
		}
	}
	noDeploy("tag che non rispetta la regola, anche se firmato", "v1.2", sigDigestC, "push", bob)
	noDeploy("non firmato", "v1.3-test.1", sigDigestD, "push", bob)
	noDeploy("firmato da un altro repo", "v1.4-test.1", sigDigestE, "push", bob)
	noDeploy("una pull request", "v1.2-test.3", sigDigestB, "pull_request", bob)
	noDeploy("chi ha fatto il push non ha `write`", "v1.2-test.3", sigDigestB, "push", users["carol"])

	// Il tag che rispetta la regola, firmato dalla CI del repo: in produzione, a nome di chi ha fatto il push.
	e.want(e.pushed(sigGitSecret, "v1.2-test.3", sigDigestB, "refs/heads/main", "push", bob), http.StatusNoContent, "")
	if got := e.versionCount(t); got != n+1 {
		t.Fatalf("versioni dopo il tag: %d, attese %d", got, n+1)
	}
	v := e.versionOf(base+"/web/versions/2", "alice")
	if v.Trigger != openapi.AppVersionTrigger("tag_push") || v.Branch != nil || !v.Image.Verified || v.CreatedBy == nil || v.CreatedBy.Username != "bob" ||
		!strings.HasSuffix(v.Image.Reference, "/web:v1.2-test.3") || v.Image.Digest == nil || *v.Image.Digest != sigDigestB {
		t.Fatalf("versione da regola sui tag: %+v", v)
	}
	// H14: commit_sha dall'etichetta revision dell'immagine (GIT-386).
	if got := e.commitInDB(t, "web", 2); got == nil || *got != tagCommitA {
		t.Fatalf("commit_sha della versione da regola sui tag = %v", got)
	}
	e.finishCommitDeploy(t, "web")

	// Un'app che segue un branch non reagisce ai tag, e viceversa una regola non reagisce ai branch.
	e.want(e.do(http.MethodPost, base, "alice", `{"name":"onbranch","image":"`+img+`","port":8081,"followBranch":"v1.2-test.3"}`), http.StatusAccepted, "")
	e.finishCommitDeploy(t, "onbranch")
	newDigest := "sha256:" + strings.Repeat("1", 64)
	e.g.tags["v1.5-test.1"] = sigTag{digest: newDigest, signer: "instance", revision: tagCommitB}
	versions := func(app string) (n int) {
		_ = e.pool.QueryRow(t.Context(), `SELECT count(*) FROM core.app_versions v JOIN core.apps a ON a.id = v.app_id WHERE a.name = $1`, app).Scan(&n)
		return n
	}
	e.want(e.pushed(sigGitSecret, "v1.5-test.1", newDigest, "refs/heads/main", "push", bob), http.StatusNoContent, "")
	if got := versions("onbranch"); got != 1 {
		t.Fatalf("l'app che segue un branch ha %d versioni: un tag non la riguarda", got)
	}
	if got := versions("web"); got != 3 {
		t.Fatalf("l'app con la regola ha %d versioni, attese 3", got)
	}
}

// Un workflow con il token del job mette in produzione un tag preciso (stessa strada di
// `gs app deploy --image <tag>` con GS_TOKEN = token del job): niente regola, nessun branch.
func TestApps_DeployDiUnTagPrecisoConIlTokenDelJob(t *testing.T) {
	e := newSigEnv(t)
	e.g.tags = map[string]sigTag{
		"ci":          {digest: sigDigestA, signer: "instance", revision: sigCommit},
		"v1.2-test.3": {digest: sigDigestB, signer: "instance", revision: tagCommitA},
	}
	e.id.grantWrite(e.repoID, "bob")
	base := "/repos/alice/handbook/apps"
	e.want(e.do(http.MethodPost, base, "alice", `{"name":"web","image":"git.example.com/alice/handbook/web:ci","port":8080,"followTagPattern":"v*-test.*"}`), http.StatusAccepted, "")
	e.finishCommitDeploy(t, "web")
	bob := users["bob"]
	e.jobToken("gsj_deploy_tag", `{"contents":"read","packages":"write","actions":"read"}`, &bob)

	rec := e.asJob(base+"/web/deploys", "gsj_deploy_tag", `{"image":"web:v1.2-test.3"}`)
	e.want(rec, http.StatusAccepted, "")
	var v openapi.AppVersion
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	if v.Number != 2 || v.Trigger != openapi.AppVersionTrigger("manual") || !v.Image.Verified || v.CreatedBy == nil || v.CreatedBy.Username != "bob" ||
		!strings.HasSuffix(v.Image.Reference, "/web:v1.2-test.3") {
		t.Fatalf("deploy del tag con il token del job: %+v", v)
	}
	// H14: anche il deploy di un tag dalla CI porta il commit dell'immagine.
	if got := e.commitInDB(t, "web", 2); got == nil || *got != tagCommitA {
		t.Fatalf("commit_sha del deploy dal job = %v", got)
	}
}
