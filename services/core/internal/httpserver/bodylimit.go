package httpserver

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// maxRequestBodyBytes è il tetto globale al corpo di una richiesta (JSON):
// 1 MiB, come quello di issue e commenti. L'upload di un allegato ha il suo
// tetto (configurabile, UploadIssueAttachment) e ne è escluso.
const maxRequestBodyBytes int64 = 1 << 20

// limitBody applica maxRequestBodyBytes a ogni richiesta prima degli
// handler: i decoder JSON generati o scritti a mano non hanno un tetto
// proprio. Il corpo viene letto in memoria (al massimo il tetto) così un
// corpo troppo grande risponde 413 body_too_large con l'errore del contratto,
// invece di un 400 generico del decoder o di un'allocazione senza fine.
func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil || r.Body == http.NoBody || isAttachmentUpload(r) {
			next.ServeHTTP(w, r)
			return
		}
		if r.ContentLength > maxRequestBodyBytes {
			writeBodyTooLarge(w)
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBodyBytes))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeBodyTooLarge(w)
				return
			}
			writeError(w, http.StatusBadRequest, "invalid_body", "Corpo della richiesta non leggibile.")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		r.ContentLength = int64(len(raw))
		next.ServeHTTP(w, r)
	})
}

// isAttachmentUpload: upload di un allegato di issue o di release. Entrambi
// hanno un tetto proprio in streaming negli handler e non si leggono in memoria.
func isAttachmentUpload(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	p := strings.TrimSuffix(r.URL.Path, "/")
	if strings.HasSuffix(p, "/issue-attachments") {
		return true
	}
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	return len(parts) == 6 && parts[0] == "repos" && parts[1] != "" && parts[2] != "" &&
		parts[3] == "releases" && parts[4] != "" && parts[5] == "assets"
}

func writeBodyTooLarge(w http.ResponseWriter) {
	writeError(w, http.StatusRequestEntityTooLarge, "body_too_large",
		"Corpo della richiesta oltre il limite di "+strconv.FormatInt(maxRequestBodyBytes, 10)+" byte.")
}
