package proxy

import (
	"net/http"
	"strings"
	"time"
)

const (
	// DefaultMaxReleaseAssetBytes è il tetto al corpo dell'upload di un
	// allegato di release: 2 GiB, lo stesso di core (maxReleaseAssetBytes in
	// releases.go). Il corpo è binario (application/octet-stream), senza
	// overhead multipart, e passa in streaming.
	DefaultMaxReleaseAssetBytes int64 = 2 << 30
	// DefaultUploadTimeout è il timeout complessivo di un upload di allegato
	// di release: non è tagliato da coreTimeout.
	DefaultUploadTimeout = 30 * time.Minute
)

// WithUploadTimeout imposta il timeout dell'upload di un allegato di release
// (e di issue). Un valore <= 0 lascia il default.
func WithUploadTimeout(d time.Duration) Option {
	return func(o *options) { o.uploadTimeout = d }
}

// isReleaseAssetUpload dice se la richiesta è
// POST [/v1]/repos/{o}/{r}/releases/{id}/assets (esatto, senza segmenti in più).
func isReleaseAssetUpload(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	p := strings.TrimPrefix(strings.TrimSuffix(r.URL.Path, "/"), PrefixV1)
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	if len(parts) != 6 {
		return false
	}
	if parts[0] != "repos" || parts[3] != "releases" || parts[5] != "assets" {
		return false
	}
	return parts[1] != "" && parts[2] != "" && parts[4] != ""
}

func isIssueAttachmentUpload(r *http.Request) bool {
	return r.Method == http.MethodPost && strings.HasSuffix(strings.TrimSuffix(r.URL.Path, "/"), "/issue-attachments")
}

// requestTimeout è il timeout della richiesta: quello degli upload per le
// rotte di upload, altrimenti quello del servizio.
func (o *options) requestTimeout(r *http.Request, def time.Duration) time.Duration {
	if isReleaseAssetUpload(r) || isIssueAttachmentUpload(r) {
		if o.uploadTimeout > 0 {
			return o.uploadTimeout
		}
		if DefaultUploadTimeout > def {
			return DefaultUploadTimeout
		}
	}
	return def
}
