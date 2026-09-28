//go:build integration

// Test d'integrazione di NATSPublisher contro un server NATS JetStream
// reale (non un mock): nats-server v2 avviato in-process
// (github.com/nats-io/nats-server/v2/server), come in pkg/events
// (pkg/events/integration_test.go, GIT-6). Non serve Docker: si esegue con
//
//	go test -tags=integration ./...
package events_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	pkgevents "github.com/fathorMB/GitStack/pkg/events"
	"github.com/fathorMB/GitStack/pkg/events/testevent"
	coreevents "github.com/fathorMB/GitStack/services/core/internal/events"
)

// startTestServer avvia un nats-server reale, in-process, con JetStream
// abilitato, e lo spegne alla fine del test. Stessa configurazione di
// pkg/events/integration_test.go.
func startTestServer(t *testing.T) *server.Server {
	t.Helper()

	opts := &server.Options{
		Host:      "127.0.0.1",
		Port:      -1, // porta libera assegnata dal SO
		JetStream: true,
		StoreDir:  t.TempDir(),
		NoLog:     true,
		NoSigs:    true,
	}
	srv, err := server.NewServer(opts)
	if err != nil {
		t.Fatalf("avvio nats-server embedded: %v", err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("nats-server embedded non pronto entro 10s")
	}
	t.Cleanup(srv.Shutdown)
	return srv
}

// jetstreamFrom apre una connessione NATS indipendente verso lo stesso
// server di nc (stessa istanza in-process) e ne ricava il contesto
// JetStream, per un consumer che non passa da NATSPublisher/core: verifica
// che ciò che NATSPublisher pubblica sia leggibile da chiunque altro parli
// pkg/events, non solo da core stesso.
func jetstreamFrom(t *testing.T, url string) jetstream.JetStream {
	t.Helper()

	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connessione NATS per il consumer: %v", err)
	}
	t.Cleanup(nc.Close)

	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("apertura contesto JetStream per il consumer: %v", err)
	}
	return js
}

// fetchOne recupera un solo messaggio dal consumer durevole, fallendo il
// test se non arriva entro il timeout dato. Stessa logica di
// pkg/events/integration_test.go.
func fetchOne(t *testing.T, cons jetstream.Consumer, timeout time.Duration) jetstream.Msg {
	t.Helper()

	batch, err := cons.Fetch(1, jetstream.FetchMaxWait(timeout))
	if err != nil {
		t.Fatalf("Fetch dal consumer durevole: %v", err)
	}
	var got jetstream.Msg
	for msg := range batch.Messages() {
		got = msg
	}
	if err := batch.Error(); err != nil {
		t.Fatalf("errore durante la consegna del batch: %v", err)
	}
	if got == nil {
		t.Fatal("nessun messaggio consumato entro il timeout")
	}
	return got
}

// TestNATSPublisher_PublishesTestResourceCreated verifica publish con
// conferma JetStream e un consumer durevole che riceve e valida lo schema
// dell'evento di prova (core.resource.test.created, v1), attraverso
// NATSPublisher: l'adapter che core usa davvero (main.go), non una sua
// riscrittura.
func TestNATSPublisher_PublishesTestResourceCreated(t *testing.T) {
	srv := startTestServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pub, nc, err := coreevents.NewNATSPublisher(ctx, srv.ClientURL())
	if err != nil {
		t.Fatalf("NewNATSPublisher: %v", err)
	}
	defer nc.Close()

	// Un consumer durevole indipendente, con la sola libreria pkg/events
	// (non passa da core): verifica che ciò che NATSPublisher pubblica sia
	// leggibile e valido per chiunque altro parli pkg/events, non solo per
	// core stesso.
	js := jetstreamFrom(t, srv.ClientURL())
	streamName, err := pkgevents.StreamName(pkgevents.Domain(testevent.Name))
	if err != nil {
		t.Fatalf("StreamName: %v", err)
	}
	cons, err := pkgevents.EnsureDurableConsumer(ctx, js, streamName, "core-test-consumer", testevent.Name)
	if err != nil {
		t.Fatalf("EnsureDurableConsumer: %v", err)
	}

	payload := coreevents.TestResourceCreatedPayload{
		ResourceID: "11111111-1111-1111-1111-111111111111",
		Type:       "repo",
		Name:       "gitstack",
	}
	if err := pub.Publish(ctx, coreevents.TestResourceCreatedName, coreevents.TestResourceCreatedVersion, payload); err != nil {
		t.Fatalf("Publish (con conferma JetStream): %v", err)
	}

	msg := fetchOne(t, cons, 5*time.Second)

	env, err := pkgevents.UnmarshalEnvelope(msg.Data())
	if err != nil {
		t.Fatalf("UnmarshalEnvelope: %v", err)
	}
	if env.Name != coreevents.TestResourceCreatedName || env.Version != coreevents.TestResourceCreatedVersion {
		t.Fatalf("envelope = %q v%d, voluto %q v%d", env.Name, env.Version, coreevents.TestResourceCreatedName, coreevents.TestResourceCreatedVersion)
	}
	if env.ID == "" || env.Time.IsZero() {
		t.Fatalf("envelope senza id o timestamp: %+v", env)
	}

	reg := pkgevents.NewRegistry()
	testevent.Register(reg)
	decoded, err := reg.Decode(env)
	if err != nil {
		t.Fatalf("Decode di uno schema noto non deve fallire: %v", err)
	}
	got, ok := decoded.(testevent.Payload)
	if !ok {
		t.Fatalf("tipo decodificato = %T, voluto testevent.Payload", decoded)
	}
	if got.ResourceID != payload.ResourceID || got.Type != payload.Type || got.Name != payload.Name {
		t.Fatalf("payload decodificato = %+v, voluto %+v", got, payload)
	}

	if err := msg.Ack(); err != nil {
		t.Fatalf("Ack del messaggio: %v", err)
	}
}

// TestNATSPublisher_UnknownVersionDoesNotCrashConsumer verifica che un
// evento con una versione di schema non registrata dal consumer venga
// riconosciuto come tale (events.UnknownSchemaError), senza panic: stesso
// criterio di GIT-6, verificato qui contro NATSPublisher.
func TestNATSPublisher_UnknownVersionDoesNotCrashConsumer(t *testing.T) {
	srv := startTestServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pub, nc, err := coreevents.NewNATSPublisher(ctx, srv.ClientURL())
	if err != nil {
		t.Fatalf("NewNATSPublisher: %v", err)
	}
	defer nc.Close()

	js := jetstreamFrom(t, srv.ClientURL())
	streamName, err := pkgevents.StreamName(pkgevents.Domain(testevent.Name))
	if err != nil {
		t.Fatalf("StreamName: %v", err)
	}
	cons, err := pkgevents.EnsureDurableConsumer(ctx, js, streamName, "core-test-consumer-unknown", testevent.Name)
	if err != nil {
		t.Fatalf("EnsureDurableConsumer: %v", err)
	}

	future := coreevents.TestResourceCreatedPayload{ResourceID: "res-future", Type: "repo", Name: "futuro"}
	if err := pub.Publish(ctx, coreevents.TestResourceCreatedName, coreevents.TestResourceCreatedVersion+1, future); err != nil {
		t.Fatalf("Publish evento con versione futura: %v", err)
	}

	msg := fetchOne(t, cons, 5*time.Second)

	env, err := pkgevents.UnmarshalEnvelope(msg.Data())
	if err != nil {
		t.Fatalf("UnmarshalEnvelope: %v", err)
	}

	reg := pkgevents.NewRegistry()
	testevent.Register(reg) // registra solo la versione 1

	_, decodeErr := reg.Decode(env)
	var unknown *pkgevents.UnknownSchemaError
	if !errors.As(decodeErr, &unknown) {
		t.Fatalf("Decode di versione sconosciuta = %v (%T), voluto *events.UnknownSchemaError", decodeErr, decodeErr)
	}
	if unknown.Version != coreevents.TestResourceCreatedVersion+1 {
		t.Fatalf("versione nell'errore = %d, voluta %d", unknown.Version, coreevents.TestResourceCreatedVersion+1)
	}

	// Nessun panic: il consumer scarta il messaggio (Term) in modo
	// controllato.
	if err := msg.Term(); err != nil {
		t.Fatalf("Term del messaggio con schema sconosciuto: %v", err)
	}
}
