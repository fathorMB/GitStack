package httpapi

import (
	"net/http"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/openapi"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// exportPageSize è la pagina con cui si leggono token e chiavi per l'export.
const exportPageSize = 100

// ExportOwnData restituisce i dati personali che identity conserva sul
// chiamante (F20, V8.3.2): profilo, token (mai il valore), chiavi SSH con la
// chiave pubblica, sessioni attive e stato del TOTP. Mai password, hash,
// segreto TOTP o codici di recupero.
func (s *server) ExportOwnData(w http.ResponseWriter, r *http.Request) {
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	out := openapi.OwnDataExport{
		ExportedAt: time.Now().UTC(),
		User:       toUser(cur.User, true),
		Tokens:     []openapi.Token{},
		SshKeys:    []openapi.ExportedSshKey{},
		Sessions:   []openapi.Session{},
	}

	if s.tokens != nil {
		for pg := 1; ; pg++ {
			list, total, err := s.tokens.List(ctx, cur.User.ID, pg, exportPageSize)
			if err != nil {
				s.internal(w, r, err)
				return
			}
			for _, t := range list {
				out.Tokens = append(out.Tokens, toToken(t))
			}
			if len(list) == 0 || pg*exportPageSize >= total {
				break
			}
		}
	}
	if s.keys != nil {
		for pg := 1; ; pg++ {
			list, total, err := s.keys.List(ctx, cur.User.ID, pg, exportPageSize)
			if err != nil {
				s.internal(w, r, err)
				return
			}
			for _, k := range list {
				out.SshKeys = append(out.SshKeys, openapi.ExportedSshKey{
					Title: k.Title, KeyType: k.KeyType, Fingerprint: k.Fingerprint, PublicKey: k.PublicKey,
					CreatedAt: k.CreatedAt, LastUsedAt: k.LastUsedAt,
				})
			}
			if len(list) == 0 || pg*exportPageSize >= total {
				break
			}
		}
	}
	sess, err := s.auth.Sessions.ListForUser(ctx, cur.User.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	for _, i := range sess {
		ua, ip := i.UserAgent, i.IPAddress
		out.Sessions = append(out.Sessions, openapi.Session{
			Id: openapi_types.UUID(i.ID), AuthMethod: openapi.SessionAuthMethod(i.AuthMethod),
			UserAgent: &ua, IpAddress: &ip, CreatedAt: i.CreatedAt, LastSeenAt: i.LastSeenAt,
			ExpiresAt: i.ExpiresAt, Current: i.ID == cur.Session.ID,
		})
	}
	if s.mfa != nil {
		enabled, err := s.mfa.Enabled(ctx, cur.User.ID)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		out.TotpEnabled = enabled
	}

	s.secInfo(r, "security.data.exported", &cur)
	w.Header().Set("Content-Disposition", `attachment; filename="gitstack-`+cur.User.Username+`.json"`)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}
