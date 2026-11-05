// Package databases e' il controller dei database gestiti (v3-d, GIT-551, ADR 0003): un reconciler dentro
// core, sul modello di `apps` (ADR 0002), non CloudNativePG. Per ogni database legge il modello Postgres e
// crea e osserva con il client Kubernetes namespace, RoleBinding, StatefulSet a 1 replica, PVC local-path,
// Service ClusterIP e NetworkPolicy; i ruoli si creano con psql via pods/exec sul socket locale.
package databases

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Config del controller.
type Config struct {
	// ControllerServiceAccount e' il ServiceAccount di core come `namespace/nome` e WorkloadRole il
	// ClusterRole modello (GIT-565, gitstack-db-workload): con entrambi il controller crea un RoleBinding in
	// ogni namespace gitstack-db-<id> (DB4). Mai un ClusterRoleBinding.
	ControllerServiceAccount string
	WorkloadRole             string
	// Images e' la tabella major -> immagine per digest del chart (DB5).
	Images map[int]Image
	// CADir contiene tls.crt e tls.key dell'intermedia dei database (GIT-565).
	CADir string
	// Verifier verifica cosign il digest prima dello StatefulSet (DB5). Nil: la creazione fallisce.
	Verifier ImageVerifier
	// SQL esegue SQL come gitstack_admin; nil usa psql via Exec.
	SQL SQLRunner
	// Exec esegue comandi nei pod (psql, pg_dump); richiesto se SQL e' nil.
	Exec Executor
	// PodLogs legge i log del pod; nil usa l'API di Kubernetes.
	PodLogs func(ctx context.Context, ns, pod string, opts *corev1.PodLogOptions) (io.ReadCloser, error)

	// Backup e ripristino (GIT-553, DB13, DB15). DumpKey e' la chiave di dump dell'istanza (Secret
	// <release>-db-dump-key, GIT-565) e DataDir la cartella databases-data; senza entrambe i backup non sono
	// disponibili (mai in chiaro). Dumper nil usa PodDumper su Exec, se e' uno StreamExecutor.
	DumpKey []byte
	DataDir string
	Dumper  Dumper
	// BackupEvery: ogni quanto il dump programmato (default 24 h); BackupRetryEvery: dopo un fallimento (1 h).
	// KeepScheduled/KeepOnDemand/KeepSafety: dump completati tenuti per tipo (default 7, 5 e 3, ADR punto 5).
	BackupEvery      time.Duration
	BackupRetryEvery time.Duration
	KeepScheduled    int
	KeepOnDemand     int
	KeepSafety       int
	// AfterRestore e' chiamato dopo un ripristino riuscito: GIT-552 vi ricrea i ruoli dei collegamenti con
	// password nuove, riscrive i Secret delle app e le riavvia (DB15, passo 4).
	AfterRestore func(ctx context.Context, d store.ManagedDB) error

	PriorityClass string
	StorageClass  string
	// LoopInterval: ogni quanto si riconciliano i database (default 3 s). UsageEvery: ogni quanto si misura
	// pg_database_size (default 30 s). ProgressTimeout: tempo massimo per arrivare a pronto (default 5 min).
	LoopInterval    time.Duration
	UsageEvery      time.Duration
	ProgressTimeout time.Duration
	Unlink          UnlinkOptions
	Now             func() time.Time
	Log             *slog.Logger
	// Secrets: portachiavi dei segreti (come le app): cifra l'intento di rotazione dei collegamenti. Nil: le
	// rotazioni interrotte non si riprendono da sole.
	Secrets store.AppSealer
	// SweepEvery: ogni quanto si cercano ruoli di collegamento senza collegamento (default LinkSweepEvery).
	SweepEvery time.Duration
	// Accesso dalla LAN (GIT-554): intervallo delle porte assegnabili (0-0: 15432-15531), porte in piu' da non
	// assegnare mai (oltre a DefaultReservedPorts) e CIDR dei pod escluso da db-lan (default DefaultPodCIDR).
	LANPortFrom, LANPortTo int
	ReservedPorts          []int
	PodCIDR                string
}

func (c *Config) defaults() {
	if c.LoopInterval <= 0 {
		c.LoopInterval = 3 * time.Second
	}
	if c.UsageEvery <= 0 {
		c.UsageEvery = 30 * time.Second
	}
	if c.ProgressTimeout <= 0 {
		c.ProgressTimeout = 5 * time.Minute
	}
	if c.SweepEvery <= 0 {
		c.SweepEvery = LinkSweepEvery
	}
	if c.Now == nil {
		c.Now = func() time.Time { return time.Now().UTC() }
	}
	if c.Log == nil {
		c.Log = slog.Default()
	}
}

// Controller riconcilia i database di core con Kubernetes.
type Controller struct {
	st   *store.Store
	kube kubernetes.Interface
	cfg  Config
	sql  SQLRunner

	kick    chan struct{}
	mu      sync.Mutex
	running map[uuid.UUID]bool
	usageAt map[uuid.UUID]time.Time
	// sweepAt: ultima ricerca di ruoli senza collegamento (mu); linkLocks: una serratura per database che
	// serializza collegamento, scollegamento e pulizia dei ruoli.
	sweepAt   map[uuid.UUID]time.Time
	linkLocks sync.Map
	// secrets sono le password appena generate, da togliere dagli errori (mu).
	secrets []string
	// tlsLoaded: impronta della catena che PostgreSQL ha ricaricato, per database (mu).
	tlsLoaded map[uuid.UUID]string
	// lanLocks: una serratura LAN per database (lanLock).
	lanLocks sync.Map
	// liveBackups sono i dump in esecuzione in questo processo (mu).
	liveBackups map[uuid.UUID]bool
	dumper      Dumper
}

// New crea il controller.
func New(st *store.Store, kube kubernetes.Interface, cfg Config) *Controller {
	cfg.defaults()
	c := &Controller{st: st, kube: kube, cfg: cfg, kick: make(chan struct{}, 1), running: map[uuid.UUID]bool{}, usageAt: map[uuid.UUID]time.Time{}, sweepAt: map[uuid.UUID]time.Time{}, liveBackups: map[uuid.UUID]bool{}, tlsLoaded: map[uuid.UUID]string{}}
	c.sql = cfg.SQL
	c.dumper = cfg.Dumper
	if se, ok := cfg.Exec.(StreamExecutor); ok && c.dumper == nil {
		c.dumper = &PodDumper{Exec: se}
	}
	if c.sql == nil && cfg.Exec != nil {
		c.sql = &PsqlRunner{Pod: cfg.Exec, Redact: c.redactions}
	}
	return c
}

func (c *Controller) remember(p string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.secrets = append(c.secrets, p)
	if len(c.secrets) > 64 {
		c.secrets = c.secrets[len(c.secrets)-64:]
	}
}

func (c *Controller) redactions() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.secrets...)
}

// SQL ritorna il runner (per i test e per chi implementa collegamenti e backup).
func (c *Controller) SQL() SQLRunner { return c.sql }

// Kick sveglia il ciclo.
func (c *Controller) Kick() {
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

// Run riconcilia finche' ctx non finisce.
func (c *Controller) Run(ctx context.Context) {
	t := time.NewTicker(c.cfg.LoopInterval)
	defer t.Stop()
	for {
		c.Once(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-c.kick:
		}
	}
}

// Once riconcilia ogni database una volta (un solo giro in parallelo per database).
func (c *Controller) Once(ctx context.Context) {
	dbs, err := c.st.ListAllManagedDBs(ctx)
	if err != nil {
		if ctx.Err() == nil {
			c.cfg.Log.Error("databases: elenco non riuscito", "err", err)
		}
		return
	}
	c.failStaleBackups(ctx)
	var wg sync.WaitGroup
	for _, d := range dbs {
		c.mu.Lock()
		busy := c.running[d.ID]
		c.running[d.ID] = true
		c.mu.Unlock()
		if busy {
			continue
		}
		wg.Add(1)
		go func(d store.ManagedDB) {
			defer wg.Done()
			defer func() { c.mu.Lock(); delete(c.running, d.ID); c.mu.Unlock() }()
			c.Reconcile(ctx, d)
		}(d)
	}
	wg.Wait()
}

// Reconcile porta un database allo stato voluto e ne scrive lo stato osservato.
func (c *Controller) Reconcile(ctx context.Context, d store.ManagedDB) {
	if d.Status == store.DBDeleting {
		actor, _ := c.st.DeletingActor(ctx, d.ID)
		if _, err := c.teardown(ctx, d, actor); err != nil && ctx.Err() == nil {
			c.cfg.Log.Warn("databases: eliminazione non completata, si ritenta", "db", d.ID, "err", err)
		}
		return
	}
	// LAN spenta: la chiusura (e la liberazione della porta) non aspetta il pod ne' il ritmo dei `failed`.
	if !d.LANEnabled || d.LANPort != nil {
		if err := c.syncLAN(ctx, d, false); err != nil && ctx.Err() == nil {
			c.cfg.Log.Warn("databases: chiusura della LAN non completata, si ritenta", "db", d.ID, "err", err)
		}
	}
	// Un database `failed` si riprova ogni minuto, non a ogni giro: cosign e il cluster non vanno martellati.
	if d.Status == store.DBFailed {
		c.mu.Lock()
		last := c.usageAt[d.ID]
		retry := c.cfg.Now().Sub(last) >= failedRetryEvery
		if retry {
			c.usageAt[d.ID] = c.cfg.Now()
		}
		c.mu.Unlock()
		if !retry {
			return
		}
	}
	status, reason, err := c.ensure(ctx, d)
	if ctx.Err() != nil {
		return
	}
	if err == nil && status == store.DBRunning {
		// Operazioni pendenti su un database pronto: ripristino, aggiornamento minor, dump giornaliero (GIT-553).
		status, reason = c.afterRunning(ctx, d)
		if ctx.Err() != nil {
			return
		}
	}
	if err != nil {
		var fatal *fatalError
		if errors.As(err, &fatal) {
			status, reason = store.DBFailed, strPtr(fatal.Error())
		} else {
			c.cfg.Log.Warn("databases: riconciliazione non riuscita, si ritenta", "db", d.ID, "err", err)
			// Un errore transitorio non sposta lo stato, ma dopo il timeout il database e' `failed` col motivo.
			if (d.Status != store.DBCreating && d.Status != store.DBUpdating) || c.cfg.Now().Sub(d.UpdatedAt) <= c.cfg.ProgressTimeout {
				return
			}
			status, reason = store.DBFailed, strPtr("il database non e' arrivato a pronto in tempo: "+err.Error())
		}
	}
	if err := c.st.SetManagedDBStatus(ctx, d.ID, status, reason, c.cfg.Now()); err != nil && ctx.Err() == nil {
		c.cfg.Log.Error("databases: stato non scritto", "db", d.ID, "err", err)
	}
}

// fatalError e' un errore che non passa da solo (immagine non verificata, CA mancante): stato `failed`.
type fatalError struct{ msg string }

func (e *fatalError) Error() string { return e.msg }

func fatalf(format string, a ...any) error { return &fatalError{msg: fmt.Sprintf(format, a...)} }

func strPtr(s string) *string { return &s }

// ensure crea quel che manca e ritorna lo stato osservato.
func (c *Controller) ensure(ctx context.Context, d store.ManagedDB) (string, *string, error) {
	img, ok := c.cfg.Images[d.PgMajor]
	if !ok {
		return "", nil, fatalf("la major %d non e' nella tabella delle immagini dell'istanza", d.PgMajor)
	}
	// DB6: l'immagine e' del singolo database. Una volta scelta e verificata resta quella anche se la
	// tabella dell'istanza cambia (nuova release): la tabella corrente vale solo per le nuove creazioni.
	// Una minor nuova arriva solo da UpgradeMinor (richiesta esplicita, dump di sicurezza riuscito).
	if d.ImageDigest != nil && d.ImageVerifiedAt != nil {
		img.Digest = *d.ImageDigest
	} else {
		// cosign PRIMA dello StatefulSet (DB5): un esito assente o negativo blocca la creazione.
		if c.cfg.Verifier == nil {
			return "", nil, fatalf("verifica cosign non configurata: l'immagine %s non puo' essere avviata", img.Ref())
		}
		if err := c.cfg.Verifier.Verify(ctx, img.Ref()); err != nil {
			return "", nil, fatalf("%v", err)
		}
		now := c.cfg.Now()
		if err := c.st.SetManagedDBImage(ctx, d.ID, img.Digest, now); err != nil {
			return "", nil, err
		}
	}
	ns := Namespace(d.ID)
	if err := c.ensureNamespace(ctx, d); err != nil {
		return "", nil, err
	}
	if err := c.ensureRoleBinding(ctx, ns); err != nil {
		return "", nil, err
	}
	if _, err := c.ensureAuthSecret(ctx, d, ""); err != nil {
		return "", nil, err
	}
	if _, err := c.ensureTLS(ctx, d); err != nil {
		return "", nil, err
	}
	if err := c.ensureOwned(ctx, ns, BuildConfigSecret(d)); err != nil {
		return "", nil, err
	}
	if err := c.ensureOwned(ctx, ns, BuildService(d)); err != nil {
		return "", nil, err
	}
	if err := c.ensureOwned(ctx, ns, BuildNetworkPolicy(d)); err != nil {
		return "", nil, err
	}
	sts, err := c.ensureStatefulSet(ctx, d, img)
	if err != nil {
		return "", nil, err
	}
	pod, _ := c.kube.CoreV1().Pods(ns).Get(ctx, PodName, metav1.GetOptions{})
	ready := sts.Status.ReadyReplicas >= 1 && sts.Status.ObservedGeneration >= sts.Generation &&
		(sts.Status.UpdateRevision == "" || sts.Status.CurrentRevision == sts.Status.UpdateRevision)
	if pod != nil {
		restarts := 0
		for _, cs := range pod.Status.ContainerStatuses {
			restarts += int(cs.RestartCount)
		}
		_ = c.st.SetManagedDBRestarts(ctx, d.ID, restarts)
		if !ready {
			if why := podWaitingReason(pod); why != "" && c.cfg.Now().Sub(d.UpdatedAt) > c.cfg.ProgressTimeout {
				return "", nil, fatalf("il pod non parte: %s", why)
			}
		}
	}
	if !ready {
		switch d.Status {
		case store.DBRunning:
			return store.DBRestarting, nil, nil
		case store.DBFailed:
			return store.DBCreating, nil, nil
		}
		return d.Status, nil, nil
	}
	if err := c.ensureRoles(ctx, d); err != nil {
		return "", nil, err
	}
	// Certificato nuovo (rinnovo o SAN della LAN): PostgreSQL lo ricarica appena e' montato, senza riavvio.
	tlsReady, err := c.reloadTLS(ctx, d)
	if err != nil {
		return "", nil, err
	}
	// LAN: lo stato voluto si rilegge sotto serratura (un PUT false riuscito non si annulla); apre solo con la
	// catena giusta caricata (GIT-554).
	if err := c.syncLAN(ctx, d, tlsReady); err != nil {
		return "", nil, err
	}
	// Collegamenti (GIT-552): riprende gli scollegamenti rimasti a meta' e toglie i ruoli senza collegamento.
	c.ResumeUnlinks(ctx, d)
	c.ResumeRotations(ctx, d)
	c.SweepOrphanRoles(ctx, d)
	c.measure(ctx, d)
	return store.DBRunning, nil, nil
}

func podWaitingReason(p *corev1.Pod) string {
	for _, cs := range p.Status.ContainerStatuses {
		if w := cs.State.Waiting; w != nil && w.Reason != "" && w.Reason != "ContainerCreating" && w.Reason != "PodInitializing" {
			return w.Reason + ": " + w.Message
		}
	}
	return ""
}

// ensureNamespace crea il namespace o ne verifica la proprieta': mai si adotta uno di altri (DB4).
func (c *Controller) ensureNamespace(ctx context.Context, d store.ManagedDB) error {
	want := BuildNamespace(d)
	cur, err := c.kube.CoreV1().Namespaces().Get(ctx, want.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = c.kube.CoreV1().Namespaces().Create(ctx, want, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			return nil
		}
		return err
	}
	if err != nil {
		return err
	}
	if cur.Labels[LabelManagedDB] != "true" || cur.Labels[LabelDBID] != d.ID.String() {
		return fatalf("il namespace %s esiste ma non e' di questo database", want.Name)
	}
	return nil
}

// ensureRoleBinding lega il ServiceAccount di core al ClusterRole modello nel solo namespace del database.
func (c *Controller) ensureRoleBinding(ctx context.Context, ns string) error {
	saNS, saName, ok := strings.Cut(c.cfg.ControllerServiceAccount, "/")
	if !ok || saNS == "" || saName == "" || c.cfg.WorkloadRole == "" {
		return nil
	}
	// DB4: il ruolo modello si lega solo in un namespace gitstack-db-* marcato managed-db (la VAP del chart
	// lo impone anche dal lato del cluster): fuori da li' il controller non crea nessun binding.
	nsObj, err := c.kube.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if !strings.HasPrefix(ns, NamePrefix) || nsObj.Labels[LabelManagedDB] != "true" {
		return fmt.Errorf("RoleBinding rifiutato: %s non e' un namespace dei database (%s=true)", ns, LabelManagedDB)
	}
	bindings := c.kube.RbacV1().RoleBindings(ns)
	_, err = bindings.Get(ctx, RoleBindingName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = bindings.Create(ctx, BuildRoleBinding(ns, saNS, saName, c.cfg.WorkloadRole), metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			return nil
		}
	}
	return err
}

const annotationRoles = "gitstack.io/roles-ready"

// annotationSANs elenca i SAN della foglia emessa (sanKey).
const annotationSANs = "gitstack.io/sans"

// ensureAuthSecret garantisce il Secret con la password del proprietario. Se manca lo crea con `password`
// (o una nuova se vuota); ritorna la password presente nel Secret.
func (c *Controller) ensureAuthSecret(ctx context.Context, d store.ManagedDB, password string) (string, error) {
	ns := Namespace(d.ID)
	secrets := c.kube.CoreV1().Secrets(ns)
	cur, err := secrets.Get(ctx, SecretAuth, metav1.GetOptions{})
	if err == nil {
		return string(cur.Data["owner-password"]), nil
	}
	if !apierrors.IsNotFound(err) {
		return "", err
	}
	if password == "" {
		if password, err = NewPassword(); err != nil {
			return "", err
		}
	}
	c.remember(password)
	_, err = secrets.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: SecretAuth, Namespace: ns, Labels: objectLabels(d.ID)},
		Data: map[string][]byte{"owner-password": []byte(password)}}, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return c.ensureAuthSecret(ctx, d, "")
	}
	return password, err
}

// Provision prepara namespace, RoleBinding e Secret con la password mostrata una volta all'utente (DB22):
// lo chiama l'API subito dopo aver scritto la riga, prima di rispondere 202.
//
// Ritorna la password EFFETTIVAMENTE persistita nel Secret: se un giro del reconciler ha gia' creato il
// Secret fra il commit della riga e questa chiamata, vale la sua password (mai sovrascritta, perche' puo'
// essere gia' applicata ai ruoli) e l'API deve mostrare quella, non la propria.
func (c *Controller) Provision(ctx context.Context, d store.ManagedDB, ownerPassword string) (string, error) {
	if err := c.ensureNamespace(ctx, d); err != nil {
		return "", err
	}
	if err := c.ensureRoleBinding(ctx, Namespace(d.ID)); err != nil {
		return "", err
	}
	pw, err := c.ensureAuthSecret(ctx, d, ownerPassword)
	if err != nil {
		return "", err
	}
	c.Kick()
	return pw, nil
}

// ensureTLS emette il certificato del server se manca o scade entro 30 giorni; ritorna true se l'ha riscritto.
func (c *Controller) ensureTLS(ctx context.Context, d store.ManagedDB) (bool, error) {
	ns := Namespace(d.ID)
	secrets := c.kube.CoreV1().Secrets(ns)
	cur, err := secrets.Get(ctx, SecretTLS, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return false, err
	}
	exists := err == nil
	dns, ips, err := c.serverSANs(d)
	if err != nil {
		if errors.Is(err, ErrNoDatabaseCA) {
			return false, fatalf("%v", err)
		}
		return false, err
	}
	wantSANs := sanKey(dns, ips)
	if exists {
		// Rinnovo a 30 giorni dalla scadenza, o quando cambiano i SAN voluti (LAN accesa o spenta, GIT-554).
		// Un certificato senza l'annotazione dei SAN e' quello col solo nome interno.
		haveSANs := cur.Annotations[annotationSANs]
		if haveSANs == "" {
			haveSANs = sanKey([]string{ServiceHost(d.ID)}, nil)
		}
		if na := cur.Annotations["gitstack.io/not-after"]; na != "" && haveSANs == wantSANs {
			if t, perr := time.Parse(time.RFC3339, na); perr == nil && t.Sub(c.cfg.Now()) > RenewBefore {
				return false, nil
			}
		}
	}
	cert, err := IssueServerCert(c.cfg.CADir, dns, ips, c.cfg.Now())
	if err != nil {
		if errors.Is(err, ErrNoDatabaseCA) {
			return false, fatalf("%v", err)
		}
		return false, err
	}
	sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: SecretTLS, Namespace: ns, Labels: objectLabels(d.ID),
		Annotations: map[string]string{"gitstack.io/not-after": cert.NotAfter.UTC().Format(time.RFC3339), annotationSANs: wantSANs}},
		Type: corev1.SecretTypeTLS, Data: map[string][]byte{"tls.crt": cert.ChainPEM, "tls.key": cert.KeyPEM}}
	if !exists {
		_, err = secrets.Create(ctx, sec, metav1.CreateOptions{})
		return true, err
	}
	cur.Data, cur.Annotations = sec.Data, sec.Annotations
	_, err = secrets.Update(ctx, cur, metav1.UpdateOptions{})
	return true, err
}

// ensureOwned crea l'oggetto se manca; se esiste verifica che sia di questo database. Secret di config,
// Service e NetworkPolicy.
func (c *Controller) ensureOwned(ctx context.Context, ns string, obj any) error {
	switch o := obj.(type) {
	case *corev1.Secret:
		cur, err := c.kube.CoreV1().Secrets(ns).Get(ctx, o.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = c.kube.CoreV1().Secrets(ns).Create(ctx, o, metav1.CreateOptions{})
			return ignoreExists(err)
		}
		if err != nil {
			return err
		}
		if string(cur.Data["pg_hba.conf"]) == string(o.Data["pg_hba.conf"]) {
			return nil
		}
		cur.Data = o.Data
		_, err = c.kube.CoreV1().Secrets(ns).Update(ctx, cur, metav1.UpdateOptions{})
		return err
	case *corev1.Service:
		cur, err := c.kube.CoreV1().Services(ns).Get(ctx, o.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = c.kube.CoreV1().Services(ns).Create(ctx, o, metav1.CreateOptions{})
			return ignoreExists(err)
		}
		_ = cur
		return err
	default:
		return c.ensureNetworkPolicy(ctx, ns, obj)
	}
}

func ignoreExists(err error) error {
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

// Delete elimina il database subito (DB19): namespace (con StatefulSet, PVC, Secret, NetworkPolicy), poi la
// riga, cosi' il nome si libera alla risposta. Un namespace di altri (id diverso) non si tocca.
func (c *Controller) Delete(ctx context.Context, d store.ManagedDB, actor *store.AppActor) error {
	if _, err := c.st.MarkManagedDBDeleting(ctx, d.ID, actor, c.cfg.Now()); err != nil {
		return err
	}
	_, err := c.teardown(ctx, d, actor)
	return err
}

// teardown toglie il namespace e, quando non c'e' piu', la riga. Ritorna true a eliminazione completa.
func (c *Controller) teardown(ctx context.Context, d store.ManagedDB, actor *store.AppActor) (bool, error) {
	// Le app collegate perdono variabili e rete prima che il database sparisca (DB19).
	if err := c.detachApps(ctx, d, actor); err != nil {
		return false, err
	}
	ns := Namespace(d.ID)
	cur, err := c.kube.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		c.removeDumps(d.ID)
		return true, c.st.RemoveManagedDB(ctx, d.ID, actor, c.cfg.Now())
	case err != nil:
		return false, err
	}
	if cur.Labels[LabelManagedDB] != "true" || cur.Labels[LabelDBID] != d.ID.String() {
		return false, fmt.Errorf("il namespace %s non e' di questo database: non si elimina", ns)
	}
	if cur.DeletionTimestamp == nil {
		pol := metav1.DeletePropagationForeground
		if err := c.kube.CoreV1().Namespaces().Delete(ctx, ns, metav1.DeleteOptions{PropagationPolicy: &pol}); err != nil && !apierrors.IsNotFound(err) {
			return false, err
		}
	}
	// Il nome si libera alla risposta (DB19): il namespace e' per id, mai riusato da un database nuovo,
	// e la sua terminazione prosegue in Kubernetes. I dump spariscono con il database.
	c.removeDumps(d.ID)
	return true, c.st.RemoveManagedDB(ctx, d.ID, actor, c.cfg.Now())
}

// RotateOwner genera una password nuova per <name>_owner, la applica e riscrive il Secret (DB22). Il
// database deve essere pronto.
func (c *Controller) RotateOwner(ctx context.Context, d store.ManagedDB) (string, error) {
	if d.Status != store.DBRunning {
		return "", ErrNotReady
	}
	pw, err := NewPassword()
	if err != nil {
		return "", err
	}
	c.remember(pw)
	if err := SetOwnerPassword(ctx, c.sql, d, pw); err != nil {
		return "", err
	}
	secrets := c.kube.CoreV1().Secrets(Namespace(d.ID))
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cur, err := secrets.Get(ctx, SecretAuth, metav1.GetOptions{})
		if err != nil {
			return err
		}
		cur.Data["owner-password"] = []byte(pw)
		_, err = secrets.Update(ctx, cur, metav1.UpdateOptions{})
		return err
	})
	return pw, err
}

// ErrNotReady: l'operazione richiede il database in `running`.
var ErrNotReady = errors.New("il database non e' ancora pronto")

// measure legge pg_database_size a intervalli e aggiorna uso e limite morbido (80% e 100%, DB3).
func (c *Controller) measure(ctx context.Context, d store.ManagedDB) {
	c.mu.Lock()
	last := c.usageAt[d.ID]
	due := c.cfg.Now().Sub(last) >= c.cfg.UsageEvery
	if due {
		c.usageAt[d.ID] = c.cfg.Now()
	}
	c.mu.Unlock()
	if !due || c.sql == nil {
		return
	}
	size, err := DatabaseSize(ctx, c.sql, d)
	if err != nil {
		c.cfg.Log.Warn("databases: pg_database_size non letto", "db", d.ID, "err", err)
		return
	}
	state := store.DiskStateFor(size, d.SizeBytes)
	changed, err := c.st.SetManagedDBUsage(ctx, d.ID, size, state)
	if err != nil {
		c.cfg.Log.Warn("databases: uso non scritto", "db", d.ID, "err", err)
		return
	}
	if changed && state != store.DiskNone {
		if err := c.st.EnqueueManagedDBQuotaWarning(ctx, d, size, state); err != nil {
			c.cfg.Log.Warn("databases: avviso di disco non scritto", "db", d.ID, "err", err)
		}
	}
}

// Logs apre i log di PostgreSQL dal pod (pods/log, DB21).
func (c *Controller) Logs(ctx context.Context, d store.ManagedDB, opts *corev1.PodLogOptions) (io.ReadCloser, error) {
	if c.cfg.PodLogs != nil {
		return c.cfg.PodLogs(ctx, Namespace(d.ID), PodName, opts)
	}
	return c.kube.CoreV1().Pods(Namespace(d.ID)).GetLogs(PodName, opts).Stream(ctx)
}

// afterRunning esegue le operazioni pendenti di un database pronto, nell'ordine: ripristino, aggiornamento minor,
// dump programmato. Ritorna lo stato da scrivere (di norma running).
func (c *Controller) afterRunning(ctx context.Context, d store.ManagedDB) (string, *string) {
	ops, err := c.st.ManagedDBOperations(ctx, d.ID)
	if err != nil {
		c.cfg.Log.Warn("databases: stato delle operazioni non letto", "db", d.ID, "err", err)
		return store.DBRunning, nil
	}
	switch {
	case ops.Restore != nil:
		return c.runRestore(ctx, d, *ops.Restore)
	case ops.UpgradeRequest != nil:
		return c.runMinorUpgrade(ctx, d, *ops.UpgradeRequest)
	}
	c.maybeScheduledBackup(ctx, d)
	return store.DBRunning, nil
}

// failedRetryEvery e' ogni quanto si riprova un database in `failed`.
const failedRetryEvery = time.Minute
