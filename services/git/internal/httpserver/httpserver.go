// Package httpserver monta il router HTTP del servizio git: /healthz e
// /readyz (probe k8s, senza firma) e l'API interna /internal/git/* chiamata
// da core, accettata solo con gli header d'identità firmati (pacchetto trust).
package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/services/git/internal/gitread"
	"github.com/fathorMB/GitStack/services/git/internal/mirrorpush"
	"github.com/fathorMB/GitStack/services/git/internal/repostore"
	"github.com/fathorMB/GitStack/services/git/internal/smarthttp"
	"github.com/fathorMB/GitStack/services/git/internal/trust"
)

// ErrUnknownTemplate è l'errore che Content restituisce per un id di modello
// sconosciuto: l'API risponde 400.
var ErrUnknownTemplate = errors.New("modello sconosciuto")

// Content produce il contenuto iniziale di un repo (R5). L'implementazione
// reale è il pacchetto templates (M-03/G).
type Content interface {
	Gitignore(id string) ([]byte, error)
	License(id string, year int, holder string) ([]byte, error)
	Readme(name, description string) []byte
}

// Store è la parte di repostore.Store usata dagli handler.
type Store interface {
	Create(ctx context.Context, id string, opts repostore.CreateOptions) (bool, error)
	Get(ctx context.Context, id string) (repostore.Info, error)
	Trash(id string) error
	Restore(id string) error
	Purge(id string) error
	Ready() error
}

// Deps sono le dipendenze del router.
type Deps struct {
	Store   Store
	Content Content
	Reads   *gitread.Service // letture sulla storia (nil = rotte non montate)
	// Mirror esegue i mirror in push (GIT-179); nil = rotta non montata.
	Mirror *mirrorpush.Service
	Secret string
	Logger *slog.Logger
	// Git, se non nil, serve lo smart HTTP su /<owner>/<repo>.git/...
	Git http.Handler
	// Now è iniettabile per i test (nil = time.Now).
	Now func() time.Time
}

// NewRouter costruisce il router.
func NewRouter(d Deps) http.Handler {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Logger == nil {
		d.Logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	h := &handler{Deps: d}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if err := d.Store.Ready(); err != nil {
			d.Logger.Warn("directory dei dati non pronta", "err", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "degraded"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	api := http.NewServeMux()
	api.HandleFunc("POST /internal/git/repos", h.create)
	api.HandleFunc("GET /internal/git/repos/{repoId}", h.get)
	api.HandleFunc("POST /internal/git/repos/{repoId}/trash", h.trash)
	api.HandleFunc("POST /internal/git/repos/{repoId}/restore", h.restore)
	api.HandleFunc("DELETE /internal/git/repos/{repoId}", h.purge)
	if d.Mirror != nil {
		api.HandleFunc("POST /internal/git/repos/{repoId}/mirror-push", h.mirrorPush)
	}
	if d.Reads != nil {
		h.mountReads(api)
	}
	mux.Handle("/internal/", trust.Require(d.Secret, nil, d.Now)(api))
	if d.Git == nil {
		return mux
	}
	// Le rotte smart HTTP hanno owner e repo variabili: si riconoscono prima
	// del mux, così un owner chiamato "internal" non collide con /internal/.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if smarthttp.Match(r.URL.Path) {
			d.Git.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

type handler struct{ Deps }

type author struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type createInput struct {
	RepoID            string  `json:"repoId"`
	Name              string  `json:"name"`
	Description       string  `json:"description"`
	DefaultBranch     string  `json:"defaultBranch"`
	Readme            bool    `json:"readme"`
	GitignoreTemplate string  `json:"gitignoreTemplate"`
	LicenseTemplate   string  `json:"licenseTemplate"`
	LicenseHolder     string  `json:"licenseHolder"`
	Author            *author `json:"author"`
}

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	var in createInput
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", "Corpo JSON non valido.")
		return
	}
	id, err := repostore.NormalizeID(in.RepoID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", "repoId non è un UUID valido.")
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		writeErr(w, http.StatusBadRequest, "invalid_request", "name è obbligatorio.")
		return
	}

	opts := repostore.CreateOptions{DefaultBranch: in.DefaultBranch, Now: h.Now()}
	if in.Readme {
		opts.Files = append(opts.Files, repostore.File{Path: "README.md", Content: h.Content.Readme(in.Name, in.Description)})
	}
	if in.GitignoreTemplate != "" {
		b, err := h.Content.Gitignore(in.GitignoreTemplate)
		if err != nil {
			h.contentErr(w, "gitignoreTemplate", err)
			return
		}
		opts.Files = append(opts.Files, repostore.File{Path: ".gitignore", Content: b})
	}
	if in.LicenseTemplate != "" {
		holder := in.LicenseHolder
		if holder == "" && in.Author != nil {
			holder = in.Author.Name
		}
		b, err := h.Content.License(in.LicenseTemplate, h.Now().Year(), holder)
		if err != nil {
			h.contentErr(w, "licenseTemplate", err)
			return
		}
		opts.Files = append(opts.Files, repostore.File{Path: "LICENSE", Content: b})
	}
	if len(opts.Files) > 0 {
		if in.Author == nil || strings.TrimSpace(in.Author.Name) == "" || strings.TrimSpace(in.Author.Email) == "" {
			writeErr(w, http.StatusBadRequest, "invalid_request", "author {name, email} è obbligatorio con il contenuto iniziale.")
			return
		}
		opts.Author = repostore.Author{Name: in.Author.Name, Email: in.Author.Email}
	}

	empty, err := h.Store.Create(r.Context(), id, opts)
	if err != nil {
		h.storeErr(w, "creazione", err)
		return
	}
	h.Logger.Info("repo creato", "repoId", id, "empty", empty)
	writeJSON(w, http.StatusCreated, map[string]any{"repoId": id, "empty": empty})
}

func (h *handler) contentErr(w http.ResponseWriter, field string, err error) {
	if errors.Is(err, ErrUnknownTemplate) {
		writeErr(w, http.StatusBadRequest, "invalid_request", field+": modello sconosciuto.")
		return
	}
	h.Logger.Error("contenuto iniziale non producibile", "field", field, "err", err)
	writeErr(w, http.StatusInternalServerError, "internal", "Errore interno.")
}

func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	id, err := repostore.NormalizeID(r.PathValue("repoId"))
	if err != nil {
		h.storeErr(w, "lettura", err)
		return
	}
	info, err := h.Store.Get(r.Context(), id)
	if err != nil {
		h.storeErr(w, "lettura", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"repoId": id, "trashed": info.Trashed, "empty": info.Empty, "branches": info.Branches})
}

func (h *handler) trash(w http.ResponseWriter, r *http.Request) {
	h.simple(w, r, "cestino", h.Store.Trash)
}

func (h *handler) restore(w http.ResponseWriter, r *http.Request) {
	h.simple(w, r, "ripristino", h.Store.Restore)
}

func (h *handler) purge(w http.ResponseWriter, r *http.Request) {
	h.simple(w, r, "cancellazione definitiva", h.Store.Purge)
}

func (h *handler) simple(w http.ResponseWriter, r *http.Request, what string, op func(string) error) {
	id, err := repostore.NormalizeID(r.PathValue("repoId"))
	if err == nil {
		err = op(id)
	}
	if err != nil {
		h.storeErr(w, what, err)
		return
	}
	h.Logger.Info("operazione sul repo eseguita", "op", what, "repoId", id)
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) storeErr(w http.ResponseWriter, what string, err error) {
	switch {
	case errors.Is(err, repostore.ErrInvalidID), errors.Is(err, repostore.ErrInvalidInput):
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
	case errors.Is(err, repostore.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", "Repo non trovato.")
	case errors.Is(err, repostore.ErrExists):
		writeErr(w, http.StatusConflict, "conflict", "Il repo esiste già.")
	case errors.Is(err, repostore.ErrNotTrashed):
		writeErr(w, http.StatusConflict, "conflict", "Il repo non è nel cestino.")
	case errors.Is(err, repostore.ErrAlreadyTrashd):
		writeErr(w, http.StatusConflict, "conflict", "Il repo è già nel cestino.")
	default:
		h.Logger.Error("operazione sul repo non riuscita", "op", what, "err", err)
		writeErr(w, http.StatusInternalServerError, "internal", "Errore interno.")
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}
