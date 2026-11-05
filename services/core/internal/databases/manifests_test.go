package databases

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

var testImage = Image{Repository: "docker.io/library/postgres", Tag: "17-alpine",
	Digest: "sha256:266d3c257594e195b309ae9d1805e54c788f1d7b375f56bf6c367aac6c917e7b"}

func testDB() store.ManagedDB {
	return store.ManagedDB{ID: uuid.MustParse("11111111-2222-3333-4444-555555555555"), OwnerName: "alice", Name: "shop", PgMajor: 17,
		SizeBytes: 1 << 30, CPUMillis: 100, MemoryBytes: 256 << 20}
}

// DB4: namespace gitstack-db-<id> con la label managed-db e Pod Security restricted.
func TestManifests_NamespaceDB4(t *testing.T) {
	d := testDB()
	ns := BuildNamespace(d)
	if ns.Name != "gitstack-db-11111111-2222-3333-4444-555555555555" {
		t.Fatalf("nome del namespace: %s", ns.Name)
	}
	if ns.Labels["gitstack.io/managed-db"] != "true" || ns.Labels["pod-security.kubernetes.io/enforce"] != "restricted" {
		t.Fatalf("label del namespace: %v", ns.Labels)
	}
}

// DB4/DB5: StatefulSet a 1 replica, PVC local-path, immagine per digest, non root uid 70, drop ALL, root fs
// in sola lettura, seccomp RuntimeDefault, PGDATA in un sottopercorso.
func TestManifests_StatefulSetDB4DB5(t *testing.T) {
	sts := BuildStatefulSet(testDB(), StatefulSetOptions{Image: testImage})
	if sts.Spec.Replicas == nil || *sts.Spec.Replicas != 1 {
		t.Fatal("serve una sola replica")
	}
	pod := sts.Spec.Template.Spec
	c := pod.Containers[0]
	if c.Image != "docker.io/library/postgres@"+testImage.Digest {
		t.Fatalf("l'immagine va per digest: %s", c.Image)
	}
	if !strings.Contains(c.Image, "@sha256:") {
		t.Fatal("niente tag mutabili")
	}
	psc := pod.SecurityContext
	if psc.RunAsUser == nil || *psc.RunAsUser != 70 || *psc.RunAsGroup != 70 || *psc.FSGroup != 70 || !*psc.RunAsNonRoot {
		t.Fatalf("il pod deve girare con uid/gid/fsGroup 70 non root: %+v", psc)
	}
	if psc.SeccompProfile == nil || psc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatalf("seccomp RuntimeDefault: %+v", psc.SeccompProfile)
	}
	sc := c.SecurityContext
	if *sc.AllowPrivilegeEscalation || !*sc.ReadOnlyRootFilesystem || len(sc.Capabilities.Drop) != 1 || sc.Capabilities.Drop[0] != "ALL" {
		t.Fatalf("contesto di sicurezza del container: %+v", sc)
	}
	if *pod.AutomountServiceAccountToken {
		t.Fatal("il pod non deve montare il token del ServiceAccount")
	}
	var pgdata string
	for _, e := range c.Env {
		if e.Name == "PGDATA" {
			pgdata = e.Value
		}
	}
	if pgdata != "/var/lib/postgresql/data/pgdata" {
		t.Fatalf("PGDATA: %q", pgdata)
	}
	vct := sts.Spec.VolumeClaimTemplates
	if len(vct) != 1 || vct[0].Spec.StorageClassName == nil || *vct[0].Spec.StorageClassName != "local-path" {
		t.Fatalf("PVC local-path: %+v", vct)
	}
	for _, name := range []string{"run", "tmp"} {
		found := false
		for _, v := range pod.Volumes {
			found = found || v.Name == name && v.EmptyDir != nil
		}
		if !found {
			t.Fatalf("emptyDir %s mancante: root fs in sola lettura", name)
		}
	}
	if c.Resources.Limits.Memory().Value() != 256<<20 || c.Resources.Requests.Cpu().MilliValue() != 100 {
		t.Fatalf("risorse: %+v", c.Resources)
	}
}

// Il ridimensionamento dichiarato non tocca il PVC (ADR punto 3): la dimensione entra solo nella richiesta
// del template, e un cambio di CPU/RAM cambia l'impronta del template (riavvio), non quella del PVC.
func TestManifests_RidimensionamentoNonEspandeIlPVC(t *testing.T) {
	d := testDB()
	a := BuildStatefulSet(d, StatefulSetOptions{Image: testImage})
	d2 := d
	d2.SizeBytes = 4 << 30
	b := BuildStatefulSet(d2, StatefulSetOptions{Image: testImage})
	if a.Annotations[annotationSpecHash] != b.Annotations[annotationSpecHash] {
		t.Fatal("cambiare solo la dimensione dichiarata non deve riavviare il pod")
	}
	d3 := d
	d3.MemoryBytes = 512 << 20
	c := BuildStatefulSet(d3, StatefulSetOptions{Image: testImage})
	if a.Annotations[annotationSpecHash] == c.Annotations[annotationSpecHash] {
		t.Fatal("cambiare la RAM deve cambiare l'impronta del template")
	}
}

// DB4: NetworkPolicy deny di default: nessuna uscita, ingresso solo dai namespace con la label di link.
func TestManifests_NetworkPolicyDenyDiDefault(t *testing.T) {
	d := testDB()
	np := BuildNetworkPolicy(d)
	if len(np.Spec.PodSelector.MatchLabels) != 0 || len(np.Spec.PodSelector.MatchExpressions) != 0 {
		t.Fatal("la policy riguarda tutti i pod del namespace")
	}
	if len(np.Spec.PolicyTypes) != 2 || len(np.Spec.Egress) != 0 {
		t.Fatalf("Ingress+Egress senza regole di uscita: %+v", np.Spec)
	}
	if len(np.Spec.Ingress) != 1 || len(np.Spec.Ingress[0].From) != 1 {
		t.Fatalf("una sola regola di ingresso: %+v", np.Spec.Ingress)
	}
	sel := np.Spec.Ingress[0].From[0].NamespaceSelector.MatchLabels
	if sel["gitstack.io/db-link-"+d.ID.String()] != "true" || len(sel) != 1 {
		t.Fatalf("ingresso solo dai namespace collegati: %v", sel)
	}
	if np.Spec.Ingress[0].From[0].IPBlock != nil {
		t.Fatal("niente blocchi IP: la LAN e' di GIT-554")
	}
}

// DB7/DB8: pg_hba.conf: gitstack_admin solo da socket locale, in rete hostssl scram-sha-256, hostnossl rifiutato.
func TestManifests_HBA_DB7DB8(t *testing.T) {
	var rules []string
	for _, l := range strings.Split(HBA, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			rules = append(rules, strings.Join(strings.Fields(l), " "))
		}
	}
	want := []string{
		"local all gitstack_admin trust",
		"local all all reject",
		"hostssl all gitstack_admin 0.0.0.0/0 reject",
		"hostssl all gitstack_admin ::/0 reject",
		"hostssl all all 0.0.0.0/0 scram-sha-256",
		"hostssl all all ::/0 scram-sha-256",
		"hostnossl all all 0.0.0.0/0 reject",
		"hostnossl all all ::/0 reject",
	}
	if strings.Join(rules, "\n") != strings.Join(want, "\n") {
		t.Fatalf("pg_hba.conf:\n%s", strings.Join(rules, "\n"))
	}
	for _, r := range rules {
		if strings.HasPrefix(r, "host ") {
			t.Fatalf("la regola %q ammetterebbe connessioni non TLS", r)
		}
		if strings.Contains(r, "trust") && !strings.HasPrefix(r, "local all gitstack_admin") {
			t.Fatalf("trust solo per gitstack_admin dal socket: %q", r)
		}
	}
}

func TestParseImages(t *testing.T) {
	imgs, err := ParseImages(`{"16":{"repository":"docker.io/library/postgres","tag":"16-alpine","digest":"sha256:` + strings.Repeat("a", 64) + `"}}`)
	if err != nil || imgs[16].Ref() != "docker.io/library/postgres@sha256:"+strings.Repeat("a", 64) {
		t.Fatalf("%v %+v", err, imgs)
	}
	if _, err := ParseImages(`{"17":{"repository":"x","tag":"17","digest":"latest"}}`); err == nil {
		t.Fatal("un digest non valido va rifiutato")
	}
}

// Il certificato del server e' firmato dall'intermedia, porta il nome interno del Service nei SAN ed e'
// verificabile contro la sola radice (la catena include l'intermedia).
func TestPKI_CertificatoFirmatoDallIntermedia(t *testing.T) {
	root, caDir := writeTestCA(t)
	d := testDB()
	cert, err := IssueServerCert(caDir, []string{ServiceHost(d.ID)}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var certs []*x509.Certificate
	rest := cert.ChainPEM
	for {
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			break
		}
		c, err := x509.ParseCertificate(b.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		certs = append(certs, c)
	}
	if len(certs) != 2 {
		t.Fatalf("catena foglia+intermedia: %d certificati", len(certs))
	}
	inter := x509.NewCertPool()
	inter.AddCert(certs[1])
	rootPool := x509.NewCertPool()
	rootPool.AddCert(root)
	if _, err := certs[0].Verify(x509.VerifyOptions{Roots: rootPool, Intermediates: inter, DNSName: "pg.gitstack-db-" + d.ID.String() + ".svc.cluster.local"}); err != nil {
		t.Fatalf("verify-full contro la radice: %v", err)
	}
	if _, err := certs[0].Verify(x509.VerifyOptions{Roots: rootPool, Intermediates: inter, DNSName: "altro.example.com"}); err == nil {
		t.Fatal("un nome diverso non deve passare")
	}
}

// DB4 (chart): il ClusterRole del controller non ha i verbi dei database; li ha solo il ClusterRole
// modello, e nessun ClusterRoleBinding (di questo chart) punta al modello: i verbi nuovi valgono solo nei
// namespace con la label, dove il controller crea un RoleBinding (VAP di GIT-565).
func TestRBAC_DB4_NessunClusterRoleBindingVersoIlModello(t *testing.T) {
	tpl := filepath.Join("..", "..", "..", "..", "deploy", "gitstack", "templates")
	raw, err := os.ReadFile(filepath.Join(tpl, "databases-rbac.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	docs := splitYAMLDocs(string(raw))
	var controllerRules, workloadRules, bindings []string
	for _, doc := range docs {
		switch {
		case isClusterRole(doc) && strings.Contains(doc, "name: {{ $controller }}\n") && !strings.Contains(doc, "role-template"):
			controllerRules = append(controllerRules, doc)
		case isClusterRole(doc) && strings.Contains(doc, "name: {{ $workload }}\n"):
			workloadRules = append(workloadRules, doc)
		}
	}
	// Tutti i ClusterRoleBinding dei template di rete e RBAC (anche delle app).
	entries, _ := os.ReadDir(tpl)
	re := regexp.MustCompile(`(?s)kind: ClusterRoleBinding.*?roleRef:.*?name: (\{\{[^}]*\}\}|\S+)`)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(tpl, e.Name()))
		for _, doc := range splitYAMLDocs(string(b)) {
			if m := re.FindStringSubmatch(doc); m != nil {
				bindings = append(bindings, e.Name()+" -> "+m[1])
			}
		}
	}
	if len(controllerRules) != 1 || len(workloadRules) != 1 {
		t.Fatalf("ClusterRole del controller=%d, modello=%d", len(controllerRules), len(workloadRules))
	}
	dbVerbs := []string{"statefulsets", "persistentvolumeclaims", "networkpolicies", "services", "secrets", "pods/exec", "pods/log"}
	for _, res := range dbVerbs {
		if strings.Contains(controllerRules[0], `"`+res+`"`) {
			t.Errorf("il ClusterRole del controller (cluster-wide) non deve avere %s", res)
		}
		if !strings.Contains(workloadRules[0], `"`+res+`"`) {
			t.Errorf("il ClusterRole modello deve avere %s", res)
		}
	}
	if len(bindings) == 0 {
		t.Fatal("nessun ClusterRoleBinding trovato: il test non guarda niente")
	}
	for _, b := range bindings {
		if strings.Contains(b, "$workload") || strings.Contains(b, "db-workload") {
			t.Errorf("ClusterRoleBinding verso il ruolo modello dei database: %s", b)
		}
	}
	vap := string(raw)
	for _, must := range []string{"gitstack.io/managed-db", "startsWith('gitstack-db-')", "roleRef.name == {{ $workload | quote }}"} {
		if !strings.Contains(vap, must) {
			t.Errorf("la VAP del chart non vincola %q", must)
		}
	}
}

func splitYAMLDocs(s string) []string {
	return regexp.MustCompile(`(?m)^---\s*$`).Split(strings.ReplaceAll(s, "\r\n", "\n"), -1)
}

// isClusterRole dice se il documento e' un ClusterRole (kind a colonna 0, non il roleRef di un binding).
func isClusterRole(doc string) bool {
	return regexp.MustCompile(`(?m)^kind: ClusterRole$`).MatchString(doc)
}
