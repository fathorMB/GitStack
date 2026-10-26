package httpserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/appenv"
	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Log delle app (v3-a, GIT-350, H5/H12). Si legge con `write` sul repo: `read` riceve 403. Le righe
// stanno nello store già mascherate dal controller; qui si rimascherano con i segreti dell'app
// (defesa in profondità: un segreto aggiunto dopo non compare in chiaro nemmeno nelle righe vecchie).

const (
	appLogsDefaultLimit    = 1000
	appLogsMaxLimit        = 10000
	appLogsMaxGrep         = 200
	appLogsFollowWindow    = time.Hour
	appLogsPoll            = time.Second
	appLogsMaxSinceSeconds = int64(10 * 365 * 24 * 3600)
)

var appLogSinceRe = regexp.MustCompile(`^([0-9]{1,9})([smhd])$`)

// appLogQuery sono i parametri letti e validati di GET .../logs.
type appLogQuery struct {
	filter store.AppLogFilter
	follow bool
	ndjson bool
}

// GetAppLogs implementa GET /repos/{owner}/{repo}/apps/{app}/logs: in diretta (follow) o cronologia.
func (s *apiServer) GetAppLogs(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, app openapi.AppParam, params openapi.GetAppLogsParams) {
	repo, _, ok := s.appRepo(w, r, owner, name, "write", false)
	if !ok {
		return
	}
	a, ok := s.appFor(w, r, repo, app)
	if !ok {
		return
	}
	q, ok := s.appLogQueryFrom(w, params)
	if !ok {
		return
	}
	mask, err := s.appLogMask(r.Context(), a.ID)
	if err != nil {
		appInternalError(w, "lettura dei segreti dell'app", err)
		return
	}
	lines, err := s.resources.ListAppLogs(r.Context(), a.ID, q.filter)
	if err != nil {
		appInternalError(w, "lettura dei log dell'app", err)
		return
	}
	aw := newAppLogWriter(w, q.ndjson)
	var cursor int64
	for _, l := range lines {
		if l.ID > cursor {
			cursor = l.ID
		}
		if err := aw.line(l, mask); err != nil {
			return
		}
	}
	aw.flush()
	if q.follow {
		s.followAppLogs(r, a.ID, q, mask, aw, cursor)
	}
}

// appLogQueryFrom legge e valida i parametri; un valore sbagliato risponde 400 con il campo.
func (s *apiServer) appLogQueryFrom(w http.ResponseWriter, params openapi.GetAppLogsParams) (appLogQuery, bool) {
	now := s.now()
	q := appLogQuery{follow: params.Follow != nil && *params.Follow}
	f := store.AppLogFilter{Limit: appLogsDefaultLimit}
	if params.Until != nil {
		u := params.Until.UTC()
		f.Until = &u
	}
	switch {
	case params.Since != nil:
		t, ok := parseAppLogSince(*params.Since, now)
		if !ok {
			writeError(w, http.StatusBadRequest, "bad_request", "since: una durata (5m, 1h, 7d) o un istante RFC 3339.")
			return appLogQuery{}, false
		}
		f.Since = &t
	case q.follow:
		t := now.Add(-appLogsFollowWindow)
		f.Since = &t
	}
	if params.Copy != nil {
		c := *params.Copy
		if c == "" || len(c) > 63 {
			writeError(w, http.StatusBadRequest, "bad_request", "copy: il nome di una copia, al massimo 63 caratteri.")
			return appLogQuery{}, false
		}
		f.Copy = &c
	}
	if params.Source != nil {
		switch string(*params.Source) {
		case "all", "":
		case "app":
			f.Source = store.AppLogSourceApp
		case "platform":
			f.Source = store.AppLogSourcePlatform
		default:
			writeError(w, http.StatusBadRequest, "bad_request", "source: all, app o platform.")
			return appLogQuery{}, false
		}
	}
	if params.Grep != nil {
		if len(*params.Grep) > appLogsMaxGrep {
			writeError(w, http.StatusBadRequest, "bad_request", "grep: al massimo 200 caratteri.")
			return appLogQuery{}, false
		}
		f.Grep = *params.Grep
	}
	if params.Limit != nil {
		if *params.Limit < 1 || *params.Limit > appLogsMaxLimit {
			writeError(w, http.StatusBadRequest, "bad_request", "limit: da 1 a 10000.")
			return appLogQuery{}, false
		}
		f.Limit = *params.Limit
	}
	if params.Format != nil {
		switch string(*params.Format) {
		case "text", "":
		case "ndjson":
			q.ndjson = true
		default:
			writeError(w, http.StatusBadRequest, "bad_request", "format: text o ndjson.")
			return appLogQuery{}, false
		}
	}
	q.filter = f
	return q, true
}

// parseAppLogSince legge una durata (`5m`, `1h`, `7d`) o un istante RFC 3339.
func parseAppLogSince(v string, now time.Time) (time.Time, bool) {
	if m := appLogSinceRe.FindStringSubmatch(v); m != nil {
		n, _ := strconv.ParseInt(m[1], 10, 64)
		unit := map[string]int64{"s": 1, "m": 60, "h": 3600, "d": 86400}[m[2]]
		secs := n * unit
		if secs > appLogsMaxSinceSeconds {
			return time.Time{}, false
		}
		return now.Add(-time.Duration(secs) * time.Second).UTC(), true
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// appLogMask costruisce il masker con i segreti dell'app. Senza portachiavi non ci sono segreti da
// leggere (il PUT dei segreti risponde 503): il masker è vuoto.
func (s *apiServer) appLogMask(ctx context.Context, appID uuid.UUID) (*ci.Masker, error) {
	if !s.hooks.Keys.Enabled() {
		return appenv.Masker(nil), nil
	}
	secrets, err := s.resources.AppSecretValues(ctx, s.hooks.Keys, appID)
	if err != nil {
		return nil, err
	}
	return appenv.Masker(secrets), nil
}

// followAppLogs tiene aperta la risposta e manda le righe nuove finché il client resta o finché
// `until` non passa. Lo store si interroga ogni secondo a partire dall'ultimo id letto.
func (s *apiServer) followAppLogs(r *http.Request, appID uuid.UUID, q appLogQuery, mask *ci.Masker, aw *appLogWriter, cursor int64) {
	f := q.filter
	f.Since, f.Limit, f.AfterID = nil, 0, cursor
	tick := time.NewTicker(appLogsPoll)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
		if f.Until != nil && !s.now().Before(*f.Until) {
			return
		}
		lines, err := s.resources.ListAppLogs(r.Context(), appID, f)
		if err != nil {
			return // la risposta è già iniziata: si chiude, il client riapre con since
		}
		for _, l := range lines {
			if l.ID > f.AfterID {
				f.AfterID = l.ID
			}
			if err := aw.line(l, mask); err != nil {
				return
			}
		}
		aw.flush()
	}
}

// appLogWriter scrive le righe come testo (`ora copia messaggio`) o come ndjson (AppLogLine).
type appLogWriter struct {
	w      http.ResponseWriter
	ndjson bool
	enc    *json.Encoder
	headed bool
}

func newAppLogWriter(w http.ResponseWriter, ndjson bool) *appLogWriter {
	return &appLogWriter{w: w, ndjson: ndjson, enc: json.NewEncoder(w)}
}

func (aw *appLogWriter) start() {
	if aw.headed {
		return
	}
	aw.headed = true
	if aw.ndjson {
		aw.w.Header().Set("Content-Type", "application/x-ndjson")
	} else {
		aw.w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	aw.w.WriteHeader(http.StatusOK)
}

func (aw *appLogWriter) line(l store.AppLogLine, mask *ci.Masker) error {
	aw.start()
	msg := mask.Mask(l.Message)
	if aw.ndjson {
		return aw.enc.Encode(openapi.AppLogLine{Time: l.Time, Source: openapi.AppLogLineSource(l.Source), Copy: l.Copy, Message: msg})
	}
	who := "platform"
	if l.Copy != nil {
		who = *l.Copy
	}
	msg = strings.NewReplacer("\r", " ", "\n", " ").Replace(msg)
	_, err := io.WriteString(aw.w, l.Time.Format(time.RFC3339Nano)+" "+who+" "+msg+"\n")
	return err
}

func (aw *appLogWriter) flush() {
	aw.start()
	_ = http.NewResponseController(aw.w).Flush()
}
