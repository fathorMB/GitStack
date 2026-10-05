package httpserver

import (
	"errors"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/fathorMB/GitStack/services/core/internal/attachments"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/google/uuid"
)

// Allegati di issue e commenti (I9, M-05/G). I file stanno sul volume degli
// allegati (internal/attachments); qui permessi, limite, tipo e intestazioni.

// AttachmentsConfig è il volume degli allegati e il limite di dimensione.
type AttachmentsConfig struct {
	Disk *attachments.Disk
	// MaxBytes: limite per file; <= 0 = attachments.DefaultMaxBytes.
	MaxBytes int64
}

func (c AttachmentsConfig) max() int64 {
	if c.MaxBytes > 0 {
		return c.MaxBytes
	}
	return attachments.DefaultMaxBytes
}

// multipartOverhead è lo spazio concesso a intestazioni e delimitatori
// multipart oltre al file.
const multipartOverhead = 64 << 10

const maxFilenameRunes = 255

// attachmentFilename riduce il nome dichiarato dal client a un nome semplice:
// ultimo segmento (anche con `\`), senza caratteri di controllo. Resta solo
// un metadato, mai parte di un percorso su disco.
func attachmentFilename(name string) string {
	name = strings.ReplaceAll(name, `\`, "/")
	name = filepath.Base(filepath.ToSlash(name))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == utf8.RuneError {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "." || name == "/" || name == "" {
		return "allegato"
	}
	if r := []rune(name); len(r) > maxFilenameRunes {
		name = string(r[:maxFilenameRunes])
	}
	return name
}

func attachmentURL(owner, repo string, id uuid.UUID) string {
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/issue-attachments/" + id.String()
}

func toAPIAttachment(owner, repo string, a store.Attachment) openapi.IssueAttachment {
	u := attachmentURL(owner, repo, a.ID)
	return openapi.IssueAttachment{
		Id: a.ID, Filename: a.Filename, ContentType: a.ContentType, Size: a.Size, CreatedAt: a.CreatedAt, Url: &u,
	}
}

// attachmentsReady: identity e volume configurati, altrimenti 503.
func (s *apiServer) attachmentsReady(w http.ResponseWriter) bool {
	if s.repoIdentity == nil {
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non configurata: impossibile applicare i permessi sui repo.")
		return false
	}
	if s.attachments.Disk == nil {
		writeError(w, http.StatusServiceUnavailable, "attachments_unavailable", "Volume degli allegati non configurato.")
		return false
	}
	return true
}

func tooLarge(err error) bool {
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe)
}

func writeTooLarge(w http.ResponseWriter, max int64) {
	writeError(w, http.StatusRequestEntityTooLarge, "attachment_too_large", "Allegato oltre il limite di "+strconv.FormatInt(max, 10)+" byte.")
}

func (s *apiServer) UploadIssueAttachment(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam) {
	_, userID, ok := callerIdentity(w, r)
	if !ok {
		return
	}
	if !s.attachmentsReady(w) {
		return
	}
	repo, ok := s.lookupReadable(w, r, userID, owner, name)
	if !ok {
		return
	}
	if repo.ArchivedAt != nil {
		writeError(w, http.StatusConflict, "archived", "Il repo è archiviato: non accetta modifiche.")
		return
	}
	max := s.attachments.max()
	mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/form-data" || params["boundary"] == "" {
		writeError(w, http.StatusBadRequest, "invalid_body", "Atteso multipart/form-data con il campo file.")
		return
	}
	if r.ContentLength > max+multipartOverhead {
		writeTooLarge(w, max)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, max+multipartOverhead)
	mr := multipart.NewReader(r.Body, params["boundary"])

	id := uuid.New()
	var (
		filename, ctype string
		size            int64
		found           bool
	)
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if tooLarge(err) {
				writeTooLarge(w, max)
				return
			}
			writeError(w, http.StatusBadRequest, "invalid_body", "Corpo multipart non valido.")
			return
		}
		if part.FormName() != "file" || part.FileName() == "" || found {
			_, _ = io.Copy(io.Discard, part)
			continue
		}
		found = true
		filename = attachmentFilename(part.FileName())
		ctype, size, err = s.attachments.Disk.Save(repo.ID, id, part, max)
		switch {
		case err == nil:
		case errors.Is(err, attachments.ErrTooLarge) || tooLarge(err):
			writeTooLarge(w, max)
			return
		case errors.Is(err, attachments.ErrUnsupported):
			writeError(w, http.StatusUnprocessableEntity, "unsupported_media_type", "Tipo di file non ammesso: immagini (PNG, JPEG, GIF, WebP), PDF, testo e log, ZIP.")
			return
		default:
			slog.Default().Warn("salvataggio dell'allegato non riuscito", "err", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante il salvataggio dell'allegato.")
			return
		}
	}
	if !found {
		writeError(w, http.StatusBadRequest, "invalid_body", "Campo file mancante.")
		return
	}
	att, err := s.resources.CreateAttachment(r.Context(), store.Attachment{
		ID: id, RepoID: repo.ID, UploaderID: userID, Filename: filename, ContentType: ctype, Size: size,
	}, s.now())
	if err != nil {
		_ = s.attachments.Disk.Remove(repo.ID, id)
		slog.Default().Warn("registrazione dell'allegato non riuscita", "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante il salvataggio dell'allegato.")
		return
	}
	out := toAPIAttachment(repo.OwnerName, repo.Name, att)
	w.Header().Set("Location", *out.Url)
	writeJSON(w, http.StatusCreated, out)
}

func (s *apiServer) GetIssueAttachment(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, attachmentID openapi.IssueAttachmentIdParam) {
	_, userID, ok := callerIdentity(w, r)
	if !ok {
		return
	}
	if !s.attachmentsReady(w) {
		return
	}
	repo, ok := s.lookupReadable(w, r, userID, owner, name)
	if !ok {
		return
	}
	notFound := func() { writeError(w, http.StatusNotFound, "not_found", "Allegato non trovato.") }
	att, err := s.resources.GetAttachment(r.Context(), repo.ID, uuid.UUID(attachmentID))
	if errors.Is(err, store.ErrNotFound) {
		notFound()
		return
	}
	if err != nil {
		slog.Default().Warn("lettura dell'allegato non riuscita", "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante la lettura dell'allegato.")
		return
	}
	// Un allegato non ancora collegato non è di nessuna issue: lo vede solo
	// chi l'ha caricato. Uno di una issue nascosta, solo chi ha admin.
	if att.IssueID == nil && att.UploaderID != userID {
		notFound()
		return
	}
	if att.IssueHidden {
		admin, err := s.repoIdentity.HasRole(r.Context(), userID, repo.ID, "admin")
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile: impossibile verificare i permessi.")
			return
		}
		if !admin {
			notFound()
			return
		}
	}
	f, err := s.attachments.Disk.Open(repo.ID, att.ID)
	if err != nil {
		slog.Default().Warn("file dell'allegato non apribile", "attachment", att.ID, "err", err)
		notFound()
		return
	}
	defer f.Close()

	// Mai una pagina (B3): il tipo viene dai byte controllati all'upload e
	// non può essere html, xml o svg; sempre attachment, nosniff e sandbox.
	h := w.Header()
	h.Set("Content-Type", att.ContentType)
	cd := mime.FormatMediaType("attachment", map[string]string{"filename": att.Filename})
	if cd == "" {
		cd = "attachment"
	}
	h.Set("Content-Disposition", cd)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox")
	h.Set("Cache-Control", "private, no-store")
	h.Set("Content-Length", strconv.FormatInt(att.Size, 10))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}
