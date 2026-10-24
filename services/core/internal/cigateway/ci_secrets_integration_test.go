//go:build integration

package cigateway_test

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	runnerv1 "code.gitea.io/actions-proto-go/runner/v1"
	"connectrpc.com/connect"
	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/fathorMB/GitStack/services/core/internal/webhooks"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const secretsKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

func testKeys(t *testing.T) *webhooks.Keyring {
	t.Helper()
	k, err := webhooks.NewKeyring("k1", secretsKey, "")
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// TestSecrets_ConsegnatiAlTaskEMascheratiNeiLog (GIT-279, CI4): i segreti di repo e di
// organizzazione (il repo prevale) arrivano al runner nel task, le variabili in `vars`, e
// nel log scritto su disco non resta né il valore né la sua forma base64/percentuale, né il token del job.
func TestSecrets_ConsegnatiAlTaskEMascheratiNeiLog(t *testing.T) {
	keys := testKeys(t)
	e := newEnvKeys(t, keys)
	ctx := context.Background()
	repo, runID := e.queue("app", "a")
	org := uuid.New()
	if _, err := e.pool.Exec(ctx, `UPDATE core.repositories SET owner_type = 'organization', owner_id = $2 WHERE resource_id = $1`, repo, org); err != nil {
		t.Fatal(err)
	}
	by := uuid.New()
	const repoSecret = "repo-segreto/Con+Simboli=42"
	const orgSecret = "org-segreto-solo-org"
	put := func(tg ci.Target, name, v string) {
		t.Helper()
		if _, err := ci.PutSecret(ctx, e.pool, keys, e.now(), tg, by, name, v); err != nil {
			t.Fatal(err)
		}
	}
	put(ci.Target{RepoID: repo}, "DEPLOY_KEY", repoSecret)
	put(ci.Target{Org: true, OrgID: org}, "DEPLOY_KEY", "org-valore-sovrascritto")
	put(ci.Target{Org: true, OrgID: org}, "ORG_ONLY", orgSecret)
	if _, err := ci.PutVariable(ctx, e.pool, e.now(), ci.Target{RepoID: repo}, by, "REGION", "eu-west"); err != nil {
		t.Fatal(err)
	}

	task, err := e.fetch(runnerSecret)
	if err != nil || task == nil {
		t.Fatal(task, err)
	}
	if task.GetSecrets()["DEPLOY_KEY"] != repoSecret || task.GetSecrets()["ORG_ONLY"] != orgSecret {
		t.Errorf("segreti nel task: %v", keysOf(task.GetSecrets()))
	}
	if task.GetVars()["REGION"] != "eu-west" {
		t.Errorf("vars: %v", task.GetVars())
	}
	tok := taskToken(t, task)

	row := func(s string) *runnerv1.LogRow { return &runnerv1.LogRow{Time: timestamppb.New(e.now()), Content: s} }
	b64 := base64.StdEncoding.EncodeToString([]byte(repoSecret))
	rows := []*runnerv1.LogRow{
		row("deploy con " + repoSecret),
		row("Authorization: Basic " + b64),
		row("org " + orgSecret + " e token " + tok),
		row("riga innocua " + "eu-west"),
	}
	if _, err := e.client.UpdateLog(ctx, authed(&runnerv1.UpdateLogRequest{TaskId: task.GetId(), Index: 0, Rows: rows}, tok)); err != nil {
		t.Fatal(err)
	}
	jobID := e.jobID(runID, "build")
	size, err := e.logs.LogSize(ctx, e.pool, jobID)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	if _, err := e.logs.ReadFrom(jobID, 0, size, &sb); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	for _, leak := range []string{repoSecret, b64, orgSecret, tok} {
		if strings.Contains(out, leak) {
			t.Errorf("il log contiene %q:\n%s", leak, out)
		}
	}
	if strings.Count(out, ci.MaskedPlaceholder) < 4 || !strings.Contains(out, "riga innocua eu-west") {
		t.Errorf("log mascherato:\n%s", out)
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		if k != "GITHUB_TOKEN" {
			out = append(out, k)
		}
	}
	return out
}

// TestSecrets_SenzaChiaveIlJobNonParte: segreti nel database ma core senza chiave: il job
// fallisce, non gira senza i suoi segreti.
func TestSecrets_SenzaChiaveIlJobNonParte(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	repo, runID := e.queue("app", "a")
	if _, err := ci.PutSecret(ctx, e.pool, testKeys(t), e.now(), ci.Target{RepoID: repo}, uuid.New(), "K", "valore-qualunque"); err != nil {
		t.Fatal(err)
	}
	task, err := e.fetch(runnerSecret)
	// Il gateway chiude il job in failure e risponde unavailable (il biglietto è già
	// consumato): senza chiave non si consegna alcun task.
	if task != nil || code(err) != connect.CodeUnavailable {
		t.Fatalf("task = %v, err = %v: senza chiave non si consegna, atteso unavailable", task, err)
	}
	// Il job non resta in coda: un nuovo giro non trova niente e non c'è loop.
	if again, err := e.fetch(runnerSecret); err != nil || again != nil {
		t.Fatalf("secondo fetch: task = %v, err = %v: il job doveva essere chiuso", again, err)
	}
	var concl string
	if err := e.pool.QueryRow(ctx, `SELECT conclusion FROM core.ci_jobs WHERE id = $1`, e.jobID(runID, "build")).Scan(&concl); err != nil || concl != "failure" {
		t.Fatalf("conclusione = %q, %v", concl, err)
	}
}
