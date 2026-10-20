// Package cigateway è il ci-gateway dell'ADR 0001 (§3-4): l'unico endpoint di core
// che il pod di un job può raggiungere, su una porta dedicata.
//
// Parla il protocollo pull runner.v1 di Gitea (actions-proto-go, ConnectRPC su HTTP):
//
//	FetchTask   long-poll; prende un job con ci.ClaimNextWithToken e gli dà il token del job
//	UpdateTask  stato dei passi ed esito; l'esito chiude il job con ci.CompleteJob
//	UpdateLog   righe di log, scritte su disco (ci.LogStore)
//
// più un solo endpoint non-RPC, GET /ci/v1/archive: l'archivio del repo del job al suo sha.
// Register e Declare (runner esterni) sono di GIT-278 e rispondono "unimplemented".
//
// Due credenziali, mai scambiabili:
//   - il segreto dei runner interni (x-runner-token o Authorization: Bearer) vale solo per FetchTask;
//   - il token del job vale solo per UpdateTask, UpdateLog e l'archivio, e solo per il job a cui
//     appartiene: un task id diverso è permission_denied, un token scaduto, revocato o sconosciuto
//     è unauthenticated.
//
// Nient'altro è raggiungibile: ogni altro percorso risponde 404.
package cigateway

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"code.gitea.io/actions-proto-go/runner/v1/runnerv1connect"
	"connectrpc.com/connect"
	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Header del protocollo runner.v1 di Gitea.
const (
	HeaderRunnerToken = "x-runner-token"
	HeaderRunnerName  = "x-runner-name"
	// ArchivePath è l'unico percorso HTTP non-RPC.
	ArchivePath = "/ci/v1/archive"
)

// Defaults.
const (
	DefaultPollTimeout = 25 * time.Second
	DefaultPollEvery   = time.Second
	maxLogRowsPerCall  = 5000
)

// WorkflowSource legge i workflow di un repo a un commit (è ci.Source senza il resto).
type WorkflowSource interface {
	Workflows(ctx context.Context, repoID uuid.UUID, sha string) ([]ci.File, error)
}

// Config configura il gateway.
type Config struct {
	Pool   *pgxpool.Pool
	Source WorkflowSource
	// Reader serve l'archivio del repo (servizio git).
	Reader gitclient.Reader
	Logs   *ci.LogStore
	// RunnerSecrets sono i segreti dei runner interni accettati da FetchTask (più d'uno: rotazione).
	// Senza, FetchTask risponde sempre unauthenticated.
	RunnerSecrets []string
	// Capacity è il numero di job in corso in tutto (CI7); 0 = ci.DefaultCapacity.
	Capacity int
	// Reporter riceve i cambi di stato di job ed esecuzioni (GIT-281), nella stessa
	// transazione del cambio. Nil = nessuno.
	Reporter ci.Reporter
	// ArchiveURL è l'indirizzo base con cui il pod raggiunge questo gateway, scritto nel
	// contesto del task; vuoto = il runner usa quello a cui ha chiamato FetchTask.
	ArchiveURL  string
	PollTimeout time.Duration
	PollEvery   time.Duration
	Now         func() time.Time
	Log         *slog.Logger
}

type gateway struct {
	Config
}

// New costruisce l'handler HTTP del gateway.
func New(cfg Config) http.Handler {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.PollTimeout <= 0 {
		cfg.PollTimeout = DefaultPollTimeout
	}
	if cfg.PollEvery <= 0 {
		cfg.PollEvery = DefaultPollEvery
	}
	g := &gateway{Config: cfg}
	mux := http.NewServeMux()
	path, h := runnerv1connect.NewRunnerServiceHandler(&rpc{g: g}, connect.WithInterceptors(g.auth()))
	mux.Handle(path, h)
	mux.HandleFunc("GET "+ArchivePath, g.archive)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Un percorso fuori dal protocollo è 404, non 405 né un redirect del mux.
		if strings.HasPrefix(r.URL.Path, path) || r.URL.Path == ArchivePath {
			mux.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})
}

type ctxKey struct{}

func authFrom(ctx context.Context) (ci.JobAuth, bool) {
	a, ok := ctx.Value(ctxKey{}).(ci.JobAuth)
	return a, ok
}

func bearer(h http.Header) string {
	if t := strings.TrimSpace(h.Get(HeaderRunnerToken)); t != "" {
		return t
	}
	if v := h.Get("Authorization"); len(v) > 7 && strings.EqualFold(v[:7], "bearer ") {
		return strings.TrimSpace(v[7:])
	}
	return ""
}

func (g *gateway) runnerSecretOK(token string) bool {
	if token == "" {
		return false
	}
	got := sha256.Sum256([]byte(token))
	ok := 0
	for _, s := range g.RunnerSecrets {
		if s == "" {
			continue
		}
		want := sha256.Sum256([]byte(s))
		ok |= subtle.ConstantTimeCompare(got[:], want[:])
	}
	return ok == 1
}

// auth è l'interceptor che applica le due credenziali per procedura.
func (g *gateway) auth() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			token := bearer(req.Header())
			switch req.Spec().Procedure {
			case runnerv1connect.RunnerServiceFetchTaskProcedure:
				if !g.runnerSecretOK(token) {
					return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("credenziale del runner non valida"))
				}
			case runnerv1connect.RunnerServiceUpdateTaskProcedure, runnerv1connect.RunnerServiceUpdateLogProcedure:
				a, err := ci.ResolveJobToken(ctx, g.Pool, g.Now(), token)
				if errors.Is(err, ci.ErrJobToken) {
					return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("token del job non valido"))
				}
				if err != nil {
					g.Log.Error("ci-gateway: verifica del token non riuscita", "err", err)
					return nil, connect.NewError(connect.CodeUnavailable, errors.New("servizio non disponibile"))
				}
				ctx = context.WithValue(ctx, ctxKey{}, a)
			default:
				// Register, Declare e qualsiasi altra procedura: GIT-278.
				return nil, connect.NewError(connect.CodeUnimplemented, errors.New("non disponibile in questa versione"))
			}
			return next(ctx, req)
		}
	}
}

// archive serve l'archivio tar.gz del repo del job al suo sha: è tutto ciò che il
// token del job dà sul codice (read), e solo per il repo del job.
func (g *gateway) archive(w http.ResponseWriter, r *http.Request) {
	a, err := ci.ResolveJobToken(r.Context(), g.Pool, g.Now(), bearer(r.Header))
	if errors.Is(err, ci.ErrJobToken) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="gitstack-ci"`)
		http.Error(w, "token del job non valido", http.StatusUnauthorized)
		return
	}
	if err != nil {
		g.Log.Error("ci-gateway: verifica del token non riuscita", "err", err)
		http.Error(w, "servizio non disponibile", http.StatusServiceUnavailable)
		return
	}
	if g.Reader == nil {
		http.Error(w, "git non disponibile", http.StatusServiceUnavailable)
		return
	}
	st, err := g.Reader.OpenStream(r.Context(), ciCaller, a.RepoID, "archive", url.Values{"ref": {a.SHA}, "format": {"tar.gz"}})
	if err != nil {
		var ae *gitclient.APIError
		if errors.As(err, &ae) {
			http.Error(w, "repository non trovato", http.StatusNotFound)
			return
		}
		g.Log.Error("ci-gateway: archivio non disponibile", "err", err)
		http.Error(w, "servizio non disponibile", http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = st.Body.Close() }()
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, st.Body)
}

// ciCaller è l'identità con cui il gateway legge da git per conto del job.
var ciCaller = trust.Identity{UserID: uuid.Nil.String(), Username: "system:ci-job"}
