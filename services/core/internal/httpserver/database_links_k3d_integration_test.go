//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/fathorMB/GitStack/services/core/internal/apps"
	"github.com/fathorMB/GitStack/services/core/internal/databases"
	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/webhooks"
)

// whoami e' l'app di prova: parte da sola e risponde; il client PostgreSQL gira in un pod di prova dello
// stesso namespace (stesse label, stessi Secret e stessa NetworkPolicy dell'app).
const whoamiBody = `{"name":%q,"image":"docker.io/traefik/whoami:v1.10.1","port":80,"healthCheck":{"path":"/","intervalSeconds":1}}`

// psqlImage e' l'immagine del client: la stessa dei database (gia' scaricata dal cluster).
const psqlImage = "docker.io/library/postgres:17-alpine"

// newLinkK3dEnv avvia insieme il controller dei database e quello delle app sullo stesso cluster k3d, con la
// radice pubblica accanto all'intermedia (come il Secret db-ca dell'installer).
func newLinkK3dEnv(t *testing.T) (*appsDeployEnv, kubernetes.Interface, *databases.SPDYExecutor) {
	t.Helper()
	kc := os.Getenv("GITSTACK_TEST_KUBECONFIG")
	if kc == "" {
		t.Skip("GITSTACK_TEST_KUBECONFIG non impostata: serve un cluster k3d (internal/databases/k3d-test.sh)")
	}
	coreImage := os.Getenv(envTestCoreImage)
	if coreImage == "" {
		t.Skip("GITSTACK_TEST_CORE_IMAGE non impostata: core rifiuta i deploy senza cancello (GIT-422)")
	}
	rc, err := clientcmd.BuildConfigFromFlags("", kc)
	if err != nil {
		t.Fatal(err)
	}
	kube, err := kubernetes.NewForConfig(rc)
	if err != nil {
		t.Fatal(err)
	}
	dyn, err := dynamic.NewForConfig(rc)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, name := range []string{databases.PriorityClass, "gitstack-apps"} {
		pc := &schedulingv1.PriorityClass{ObjectMeta: metav1.ObjectMeta{Name: name}, Value: 100000}
		if _, err := kube.SchedulingV1().PriorityClasses().Create(ctx, pc, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatal(err)
		}
	}
	caDir, rootPEM := k3dCA(t)
	if err := os.WriteFile(filepath.Join(caDir, databases.RootCAFileName), rootPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	pool, _ := dbtest.NewPool(t)
	id := &issuesIdentity{fakeIdentity: newFakeIdentity(), writers: map[uuid.UUID][]string{}}
	base := &issuesEnv{t: t, pool: pool, id: id}
	git := newFakeGit()
	clock := &appClock{t: time.Now().UTC().Truncate(time.Second)}
	keys, err := webhooks.NewKeyring("k1", hookSecretKey, "")
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(pool)
	actl := apps.New(st, kube, dyn, apps.Config{AppsDomain: "apps.example.com", ProgressTimeout: 90 * time.Second, FailureGrace: 6 * time.Second,
		PollInterval: 500 * time.Millisecond, LoopInterval: time.Second, SyncEvery: 2 * time.Second, Secrets: keys, GateImage: coreImage,
		DatabaseCAFile: filepath.Join(caDir, databases.RootCAFileName)})
	exec := &databases.SPDYExecutor{Config: rc, Client: kube}
	img := map[int]databases.Image{17: {Repository: "docker.io/library/postgres", Tag: "17-alpine", Digest: "sha256:266d3c257594e195b309ae9d1805e54c788f1d7b375f56bf6c367aac6c917e7b"}}
	dctl := databases.New(st, kube, databases.Config{Images: img, CADir: caDir, Exec: exec, Verifier: okVerifier{},
		LoopInterval: time.Second, UsageEvery: 5 * time.Second, ProgressTimeout: 5 * time.Minute})
	base.router = httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret,
		httpserver.WithRepoIdentity(id), httpserver.WithReadableLister(id), httpserver.WithGit(git),
		httpserver.WithCloneConfig(httpserver.CloneConfig{PublicURL: "https://git.example.com"}),
		httpserver.WithAppsDomain("apps.example.com"), httpserver.WithClock(clock.Now),
		httpserver.WithWebhooks(httpserver.WebhookConfig{Keys: keys}), httpserver.WithAppController(actl),
		httpserver.WithDatabases(httpserver.DatabasesConfig{Controller: dctl}))
	runCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	go actl.Run(runCtx)
	go dctl.Run(runCtx)
	return &appsDeployEnv{appsEnv: &appsEnv{issuesEnv: base, clock: clock, git: git}, kube: kube, dyn: dyn}, kube, exec
}

// pod di prova: gira nel namespace di un'app, con le sue label (la NetworkPolicy app-isolation vale), il cancello di
// rete e, se envFrom, il Secret app-secrets e la CA montata come nel Deployment dell'app.
func linkProbe(t *testing.T, kube kubernetes.Interface, ns, name, script string, fromApp bool, env map[string]string) string {
	t.Helper()
	spec := corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, Containers: []corev1.Container{{
		Name: "probe", Image: psqlImage, Command: []string{"sh", "-c", script}}},
		InitContainers: []corev1.Container{apps.GateContainer(os.Getenv(envTestCoreImage), apps.GateCommand)}}
	c := &spec.Containers[0]
	if fromApp {
		c.EnvFrom = []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "app-secrets"}}}}
		spec.Volumes = []corev1.Volume{{Name: "db-ca", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: apps.DatabaseCASecret}}}}
		c.VolumeMounts = []corev1.VolumeMount{{Name: "db-ca", MountPath: databases.CAMountDir, ReadOnly: true}}
	}
	for k, v := range env {
		c.Env = append(c.Env, corev1.EnvVar{Name: k, Value: v})
	}
	if _, err := kube.CoreV1().Pods(ns).Create(context.Background(), &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: map[string]string{apps.LabelManaged: "true"}}, Spec: spec}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	return waitPodLog(t, kube, ns, name)
}

// connectOK: si connette con DATABASE_URL (verify-full, CA montata) come farebbe l'app; ritenta, perche' un pod
// appena creato non e' subito ammesso dalla NetworkPolicy (ADR, «Avvertenza osservata»).
func connectOK(sql string) string {
	return retryProbeScript(fmt.Sprintf(`psql "$DATABASE_URL" -X -v ON_ERROR_STOP=1 -t -A -c %q`, sql), "echo OK")
}

// TestDatabaseLinks_ReteVariabiliERotazioneSuK3d prova, su un cluster vero, i criteri di GIT-552:
//   - l'app collegata si connette con DATABASE_URL senza configurazione a mano (verify-full con la CA montata);
//     l'app non collegata, anche dello stesso repo, non raggiunge il database;
//   - il collegamento da un repo diverso dello stesso owner funziona;
//   - l'app con l'uscita ristretta raggiunge comunque il database collegato (e non internet);
//   - la rotazione fa ripartire l'app con la password nuova, la vecchia non entra piu' e i dati restano;
//   - scollegare toglie variabili, label e rete; eliminare il database toglie le variabili alle app.
func TestDatabaseLinks_ReteVariabiliERotazioneSuK3d(t *testing.T) {
	e, kube, _ := newLinkK3dEnv(t)
	ctx := context.Background()
	deadline := time.Now().Add(6 * time.Minute)

	created := (&dbEnv{appsEnv: e.appsEnv}).create("alice", "alice", `{"name":"lk","pgMajor":17}`, http.StatusAccepted)
	dbID := uuid.UUID(created.Database.Id)
	dbNS := databases.Namespace(dbID)
	t.Cleanup(func() { _ = kube.CoreV1().Namespaces().Delete(ctx, dbNS, metav1.DeleteOptions{}) })
	for {
		rec := e.do(http.MethodGet, "/databases/alice/lk", "alice", "")
		var d openapi.Database
		_ = json.Unmarshal(rec.Body.Bytes(), &d)
		if d.Status == "running" {
			break
		}
		if d.Status == "failed" || time.Now().After(deadline) {
			t.Fatalf("database in stato %s: %s", d.Status, rec.Body.String())
		}
		time.Sleep(2 * time.Second)
	}

	e.repo("r1", true)
	e.repo("r2", true)
	app := func(repo, name string) (path, ns string, id uuid.UUID) {
		e.want(e.do(http.MethodPost, "/repos/alice/"+repo+"/apps", "alice", fmt.Sprintf(whoamiBody, name)), http.StatusAccepted, "")
		path = "/repos/alice/" + repo + "/apps/" + name
		a := e.waitApp(path, repo+"/"+name, func(a openapi.App) bool { return a.Status == "running" })
		id = uuid.UUID(a.Id)
		ns = apps.Namespace(id)
		t.Cleanup(func() { _ = kube.CoreV1().Namespaces().Delete(ctx, ns, metav1.DeleteOptions{}) })
		return
	}
	webPath, webNS, webID := app("r1", "web")
	_, otherNS, _ := app("r1", "other")
	apiPath, apiNS, _ := app("r2", "api")

	link := func(repo, name, mode string) openapi.DatabaseLinkCreated {
		rec := e.do(http.MethodPost, "/databases/alice/lk/links", "alice", fmt.Sprintf(`{"app":{"owner":"alice","repo":%q,"name":%q},"mode":%q}`, repo, name, mode))
		e.want(rec, http.StatusCreated, "")
		var out openapi.DatabaseLinkCreated
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	webLink := link("r1", "web", "rw")
	apiLink := link("r2", "api", "ro") // repo diverso dello stesso owner
	e.waitState(webPath, 2, "serving")
	e.waitState(apiPath, 2, "serving")

	// Cluster: label sul namespace delle sole app collegate, CA e variabili nel Secret, Deployment che le monta.
	ns := func(name string) *corev1.Namespace {
		n, err := kube.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	label := databases.LinkLabel(dbID)
	if ns(webNS).Labels[label] != "true" || ns(apiNS).Labels[label] != "true" || ns(otherNS).Labels[label] != "" {
		t.Fatalf("label %s: web=%v api=%v other=%v", label, ns(webNS).Labels, ns(apiNS).Labels, ns(otherNS).Labels)
	}
	sec, err := kube.CoreV1().Secrets(webNS).Get(ctx, "app-secrets", metav1.GetOptions{})
	if err != nil || string(sec.Data["PGPASSWORD"]) != webLink.Credentials.Password || !strings.Contains(string(sec.Data["DATABASE_URL"]), "verify-full") {
		t.Fatalf("Secret dell'app: %v %v", err, sec)
	}
	if ca, err := kube.CoreV1().Secrets(webNS).Get(ctx, apps.DatabaseCASecret, metav1.GetOptions{}); err != nil || len(ca.Data["ca.crt"]) == 0 {
		t.Fatalf("Secret della CA: %v", err)
	}
	dep, err := kube.AppsV1().Deployments(webNS).Get(ctx, apps.WorkloadName, metav1.GetOptions{})
	if err != nil || len(dep.Spec.Template.Spec.Containers[0].EnvFrom) != 1 {
		t.Fatalf("Deployment: %v", err)
	}
	if strings.Contains(fmt.Sprintf("%+v", dep.Spec), webLink.Credentials.Password) {
		t.Fatal("il Deployment contiene la password in chiaro")
	}
	if _, err := kube.CoreV1().Secrets(otherNS).Get(ctx, "app-secrets", metav1.GetOptions{}); err == nil {
		t.Fatal("l'app non collegata non ha variabili del database")
	}

	// 1) L'app collegata (rw) si connette con DATABASE_URL e scrive; l'app di un altro repo (ro) legge e non scrive.
	if got := linkProbe(t, kube, webNS, "rw", connectOK("create table if not exists prova (x int); insert into prova values (1)"), true, nil); got != "OK" {
		t.Fatalf("app collegata rw: %s", got)
	}
	if got := linkProbe(t, kube, apiNS, "ro-legge", connectOK("select count(*) from prova"), true, nil); got != "OK" {
		t.Fatalf("app collegata ro da un altro repo: %s", got)
	}
	roWrite := `if psql "$DATABASE_URL" -X -v ON_ERROR_STOP=1 -c "insert into prova values (2)" >/dev/null 2>&1; then echo KO; exit 1; else echo OK; fi`
	if got := linkProbe(t, kube, apiNS, "ro-scrive", roWrite, true, nil); got != "OK" {
		t.Fatalf("il collegamento ro non deve scrivere: %s", got)
	}
	// L'app non collegata dello stesso repo non raggiunge il database (controllo opposto: la collegata si'). Il pod
	// del collegato ha gia' dimostrato la raggiungibilita' dello stesso host e porta.
	host := databases.ServiceHost(dbID)
	if got := linkProbe(t, kube, otherNS, "non-collegata", ncProbeScript(host, 5432, false, "echo KO; exit 1", "echo OK"), false, nil); got != "OK" {
		t.Fatalf("l'app non collegata raggiunge il database: %s", got)
	}

	// 2) Uscita ristretta (nessuna destinazione): l'app raggiunge comunque il database collegato, e non internet.
	e.want(e.do(http.MethodPut, webPath+"/egress", "alice", `{"destinations":[]}`), http.StatusOK, "")
	if got := linkProbe(t, kube, webNS, "ristretta-db", connectOK("select 1"), true, nil); got != "OK" {
		t.Fatalf("app con uscita ristretta verso il database: %s", got)
	}
	if got := linkProbe(t, kube, webNS, "ristretta-internet", ncProbeScript("1.1.1.1", 443, false, "echo KO; exit 1", "echo OK"), false, nil); got != "OK" {
		t.Fatalf("la restrizione deve chiudere internet: %s", got)
	}

	// 3) Rotazione: password nuova nel Secret, app ripartita (nuova versione), la vecchia non entra, i dati restano.
	oldURL := string(sec.Data["DATABASE_URL"])
	rec := e.do(http.MethodPost, "/databases/alice/lk/links/"+uuid.UUID(webLink.Link.Id).String()+"/rotate", "alice", "")
	e.want(rec, http.StatusOK, "")
	var rot openapi.DatabaseLinkRotated
	_ = json.Unmarshal(rec.Body.Bytes(), &rot)
	e.waitState(webPath, 3, "serving") // la restrizione dell uscita non crea versioni: 3 e la rotazione
	sec, err = kube.CoreV1().Secrets(webNS).Get(ctx, "app-secrets", metav1.GetOptions{})
	if err != nil || string(sec.Data["PGPASSWORD"]) != rot.Credentials.Password {
		t.Fatalf("Secret dopo la rotazione: %v", err)
	}
	if got := linkProbe(t, kube, webNS, "nuova", connectOK("select count(*) from prova where x = 1"), true, nil); got != "OK" {
		t.Fatalf("app con la password nuova: %s", got)
	}
	oldScript := `if psql "$OLD_URL" -X -v ON_ERROR_STOP=1 -c "select 1" >/dev/null 2>&1; then echo KO; exit 1; else echo OK; fi`
	if got := linkProbe(t, kube, webNS, "vecchia", oldScript, true, map[string]string{"OLD_URL": oldURL}); got != "OK" {
		t.Fatalf("la password vecchia entra ancora: %s", got)
	}

	// 4) Scollegare toglie variabili, label e regola di rete.
	e.want(e.do(http.MethodDelete, "/databases/alice/lk/links/"+uuid.UUID(apiLink.Link.Id).String(), "alice", ""), http.StatusAccepted, "")
	for {
		list := e.do(http.MethodGet, "/databases/alice/lk/links", "alice", "").Body.String()
		if !strings.Contains(list, uuid.UUID(apiLink.Link.Id).String()) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("scollegamento non completato: %s", list)
		}
		time.Sleep(time.Second)
	}
	e.waitState(apiPath, 3, "serving")
	if ns(apiNS).Labels[label] != "" {
		t.Fatalf("label rimasta dopo lo scollegamento: %v", ns(apiNS).Labels)
	}
	if _, err := kube.CoreV1().Secrets(apiNS).Get(ctx, "app-secrets", metav1.GetOptions{}); err == nil {
		t.Fatal("variabili rimaste dopo lo scollegamento")
	}
	if _, err := kube.CoreV1().Secrets(apiNS).Get(ctx, apps.DatabaseCASecret, metav1.GetOptions{}); err == nil {
		t.Fatal("CA rimasta dopo lo scollegamento")
	}
	if np, err := kube.NetworkingV1().NetworkPolicies(apiNS).Get(ctx, "app-isolation", metav1.GetOptions{}); err != nil {
		t.Fatal(err)
	} else if strings.Contains(fmt.Sprintf("%+v", np.Spec.Egress), dbNS) {
		t.Fatal("la regola di uscita verso il database e' rimasta")
	}
	if got := linkProbe(t, kube, apiNS, "scollegata", ncProbeScript(host, 5432, false, "echo KO; exit 1", "echo OK"), false, nil); got != "OK" {
		t.Fatalf("l'app scollegata raggiunge ancora il database: %s", got)
	}
	// L'altra app collegata continua.
	if got := linkProbe(t, kube, webNS, "continua", connectOK("select 1"), true, nil); got != "OK" {
		t.Fatalf("l'altra app collegata ha perso il database: %s", got)
	}

	// 5) Eliminare il database toglie le variabili all'app che restava collegata.
	e.want(e.do(http.MethodDelete, "/databases/alice/lk?confirm=alice/lk", "alice", ""), http.StatusNoContent, "")
	e.waitState(webPath, 4, "serving")
	if _, err := kube.CoreV1().Secrets(webNS).Get(ctx, "app-secrets", metav1.GetOptions{}); err == nil {
		t.Fatal("l'app ha ancora le variabili del database eliminato")
	}
	if got := e.secretsOf(webID); len(got) != 0 {
		t.Fatalf("segreti dell'app dopo l'eliminazione del database: %v", got)
	}
}

func (e *appsDeployEnv) secretsOf(id uuid.UUID) map[string]string {
	e.t.Helper()
	keys, _ := webhooks.NewKeyring("k1", hookSecretKey, "")
	v, err := store.New(e.pool).AppSecretValues(context.Background(), keys, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}
