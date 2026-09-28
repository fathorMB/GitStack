// Package events è la libreria condivisa di GitStack per pubblicare e
// consumare eventi sul bus NATS JetStream (decisione D11 [c_3db1353f49975f16],
// base futura per git.push, issue.*, CI e deploy [c_4ef209e79c5b2ddb]).
//
// Ogni evento viaggia in un Envelope con nome, versione di schema, id
// univoco, timestamp e payload applicativo in JSON (vedi Envelope). Il nome
// dell'evento è anche il subject NATS su cui viene pubblicato. Le
// convenzioni di nomi di subject e stream sono documentate in
// docs/events.md, non qui, perché riguardano anche i servizi che questo
// pacchetto non conosce (git, core, identity...).
//
// Publisher pubblica con conferma sincrona di JetStream (Publish ritorna
// solo dopo l'ack del server). Registry valida lo schema lato consumer:
// un nome o una versione non registrati non fanno fallire la decodifica in
// modo brusco, ritornano un *UnknownSchemaError che il chiamante decide
// come gestire (log, Term del messaggio, dead-letter), senza crash.
//
// pkg/events/testevent definisce l'evento di prova usato per validare
// l'intera catena (publish con conferma, consumer durevole, validazione di
// schema) in questo pacchetto e, in GIT-5, per farlo emettere dal servizio
// core alla creazione della risorsa di prova.
package events
