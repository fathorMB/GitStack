// Command core implementa la risorsa di prova del contratto OpenAPI
// pubblico di GitStack (api/openapi.yaml) su uno schema Postgres dedicato
// ("core", D6 [c_4df04d65b3ac4910]): API di repo, issues, commenti,
// etichette, milestone, notifiche e webhook arrivano con le milestone
// successive (vedi README.md). In M-01 (T-05) c'è solo lo scheletro
// end-to-end: CRUD della risorsa generica, migrazioni versionate,
// health/readiness che verificano il database, e la pubblicazione
// dell'evento di prova quando la libreria eventi condivisa (GIT-6) è
// disponibile.
package main
