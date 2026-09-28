//go:build integration

// Suite d'integrazione contro un server NATS JetStream reale (non un mock):
// avvia un nats-server v2 in-process con JetStream abilitato
// (github.com/nats-io/nats-server/v2/server), storage su una cartella
// temporanea per test, protocollo e motore JetStream identici a quelli di
// un nats-server standalone. Non serve Docker né una rete esterna: si
// esegue con
//
//	go test -tags=integration ./...
//
// dentro pkg/events. In CI basta lanciare lo stesso comando: nessun
// servizio da orchestrare a parte. Se in futuro si preferisce un NATS
// esterno vero (container o servizio CI dedicato), basta sostituire
// startTestServer con un client che punta al suo URL: il resto del test
// (publish, consumer durevole, validazione di schema) non cambia perché
// parla solo con l'interfaccia jetstream.JetStream.
package events_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/fathorMB/GitStack/pkg/events"
	"github.com/fathorMB/GitStack/pkg/events/testevent"
)

// startTestServer avvia un nats-server reale, in-process, con JetStream
// abilitato, e lo spegne alla fine del test.
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

// connectJetStream apre una connessione NATS verso srv e ne ricava il
// contesto JetStream, chiudendo entrambi alla fine del test.
func connectJetStream(t *testing.T, srv *server.Server) jetstream.JetStream {
	t.Helper()

	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatalf("connessione a NATS %q: %v", srv.ClientURL(), err)
	}
	t.Cleanup(nc.Close)

	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("apertura contesto JetStream: %v", err)
	}
	return js
}

// fetchOne recupera un solo messaggio dal consumer durevole, fallendo il
// test se non arriva entro il timeout dato.
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

// TestPublishAndConsume_TestEvent copre publish con conferma JetStream,
// consumer durevole che riceve il messaggio e validazione di schema lato
// consumer per l'evento di prova (criteri di GIT-6), contro un NATS
// JetStream reale.
func TestPublishAndConsume_TestEvent(t *testing.T) {
	srv := startTestServer(t)
	js := connectJetStream(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	domain := events.Domain(testevent.Name)
	streamName, err := events.StreamName(domain)
	if err != nil {
		t.Fatalf("StreamName: %v", err)
	}
	if _, err := events.EnsureStream(ctx, js, domain); err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}

	cons, err := events.EnsureDurableConsumer(ctx, js, streamName, "test-consumer", testevent.Name)
	if err != nil {
		t.Fatalf("EnsureDurableConsumer: %v", err)
	}

	pub := events.NewPublisher(js)
	payload := testevent.Payload{ResourceID: "res-1", Type: "test", Name: "demo"}
	result, err := pub.Publish(ctx, testevent.Name, testevent.Version, payload)
	if err != nil {
		t.Fatalf("Publish (con conferma JetStream): %v", err)
	}
	if result.Stream != streamName {
		t.Fatalf("stream confermato = %q, voluto %q", result.Stream, streamName)
	}
	if result.Sequence == 0 {
		t.Fatal("sequence confermata da JetStream = 0, atteso > 0")
	}

	msg := fetchOne(t, cons, 5*time.Second)

	env, err := events.UnmarshalEnvelope(msg.Data())
	if err != nil {
		t.Fatalf("UnmarshalEnvelope: %v", err)
	}
	if env.Name != testevent.Name || env.Version != testevent.Version {
		t.Fatalf("envelope = %q v%d, voluto %q v%d", env.Name, env.Version, testevent.Name, testevent.Version)
	}
	if env.ID == "" || env.Time.IsZero() {
		t.Fatalf("envelope senza id o timestamp: %+v", env)
	}

	reg := events.NewRegistry()
	testevent.Register(reg)

	decoded, err := reg.Decode(env)
	if err != nil {
		t.Fatalf("Decode di uno schema noto non deve fallire: %v", err)
	}
	got, ok := decoded.(testevent.Payload)
	if !ok {
		t.Fatalf("tipo decodificato = %T, voluto testevent.Payload", decoded)
	}
	if got != payload {
		t.Fatalf("payload decodificato = %+v, voluto %+v", got, payload)
	}

	if err := msg.Ack(); err != nil {
		t.Fatalf("Ack del messaggio: %v", err)
	}
}

// TestConsumer_UnknownVersionHandledWithoutCrash verifica che un evento con
// una versione di schema non registrata venga scartato in modo controllato
// (nessun panic, nessun crash del processo), e che il consumer resti in
// grado di elaborare i messaggi successivi.
func TestConsumer_UnknownVersionHandledWithoutCrash(t *testing.T) {
	srv := startTestServer(t)
	js := connectJetStream(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	domain := events.Domain(testevent.Name)
	streamName, err := events.StreamName(domain)
	if err != nil {
		t.Fatalf("StreamName: %v", err)
	}
	if _, err := events.EnsureStream(ctx, js, domain); err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}
	cons, err := events.EnsureDurableConsumer(ctx, js, streamName, "test-consumer-unknown", testevent.Name)
	if err != nil {
		t.Fatalf("EnsureDurableConsumer: %v", err)
	}

	pub := events.NewPublisher(js)

	// Evento con una versione di schema che nessun consumer conosce ancora
	// (es. pubblicato da un core più nuovo di questo consumer).
	future := testevent.Payload{ResourceID: "res-2", Type: "test", Name: "futuro"}
	if _, err := pub.Publish(ctx, testevent.Name, testevent.Version+1, future); err != nil {
		t.Fatalf("Publish evento con versione futura: %v", err)
	}

	reg := events.NewRegistry()
	testevent.Register(reg) // registra solo la versione 1

	msg := fetchOne(t, cons, 5*time.Second)
	env, err := events.UnmarshalEnvelope(msg.Data())
	if err != nil {
		t.Fatalf("UnmarshalEnvelope: %v", err)
	}

	_, decodeErr := reg.Decode(env)
	var unknown *events.UnknownSchemaError
	if !errors.As(decodeErr, &unknown) {
		t.Fatalf("Decode di versione sconosciuta = %v (%T), voluto *events.UnknownSchemaError", decodeErr, decodeErr)
	}
	if unknown.Version != testevent.Version+1 {
		t.Fatalf("versione nell'errore = %d, voluta %d", unknown.Version, testevent.Version+1)
	}

	// Il consumer scarta il messaggio (Term, non Nak: non ha senso
	// riconsegnarlo, nessuna versione futura del processo lo capirà) senza
	// che il processo sia mai andato in panic.
	if err := msg.Term(); err != nil {
		t.Fatalf("Term del messaggio con schema sconosciuto: %v", err)
	}

	// Un evento successivo, con uno schema noto, deve ancora arrivare: il
	// consumer non è rimasto bloccato o è crashato per colpa del primo.
	known := testevent.Payload{ResourceID: "res-3", Type: "test", Name: "noto"}
	if _, err := pub.Publish(ctx, testevent.Name, testevent.Version, known); err != nil {
		t.Fatalf("Publish evento con versione nota: %v", err)
	}
	msg2 := fetchOne(t, cons, 5*time.Second)
	env2, err := events.UnmarshalEnvelope(msg2.Data())
	if err != nil {
		t.Fatalf("UnmarshalEnvelope (secondo evento): %v", err)
	}
	decoded2, err := reg.Decode(env2)
	if err != nil {
		t.Fatalf("Decode del secondo evento (schema noto) non deve fallire: %v", err)
	}
	got2, ok := decoded2.(testevent.Payload)
	if !ok || got2 != known {
		t.Fatalf("secondo evento decodificato = %+v (ok=%v), voluto %+v", decoded2, ok, known)
	}
	if err := msg2.Ack(); err != nil {
		t.Fatalf("Ack del secondo messaggio: %v", err)
	}
}
