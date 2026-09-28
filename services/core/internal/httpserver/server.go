package httpserver

import (
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

// apiServer implementa openapi.ServerInterface: le operazioni del contratto
// leggono/scrivono sullo store Postgres (schema dedicato "core", D6) e, per
// la creazione, pubblicano l'evento di prova tramite events.Publisher.
// Implementare l'interfaccia generata dal contratto, invece di definire le
// rotte a mano, impedisce a core di discostarsi dal contratto: se il
// contratto cambia, il generato cambia, e questo file smette di compilare
// finché non lo si adegua (stessa convenzione del gateway, vedi
// services/gateway/internal/httpserver/server.go).
type apiServer struct {
	pool      *pgxpool.Pool
	resources *store.Store
	events    events.Publisher
}

var _ openapi.ServerInterface = (*apiServer)(nil)
