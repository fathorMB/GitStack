//go:build integration

package httpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/attachments"
	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
)

var (
	pngBytes = append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), make([]byte, 40)...)
	zipBytes = append([]byte("PK\x03\x04"), make([]byte, 40)...)
)

const attachMax = 1024

type attachEnv struct {
	*reposEnv
	dir string
}

func newAttachEnv(t *testing.T) *attachEnv {
	t.Helper()
	return newAttachEnvQuota(t, 0, 0)
}

// newAttachEnvQuota è newAttachEnv con le quote di repo e utente (0 = default).
func newAttachEnvQuota(t *testing.T, repoQuota, userQuota int64) *attachEnv {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	dir := t.TempDir()
	id := newFakeIdentity()
	git := newFakeGit()
	router := httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret,
		httpserver.WithRepoIdentity(id), httpserver.WithReadableLister(id), httpserver.WithGit(git),
		httpserver.WithAttachments(httpserver.AttachmentsConfig{Disk: &attachments.Disk{Dir: dir}, MaxBytes: attachMax, MaxRepoBytes: repoQuota, MaxUserBytes: userQuota}))
	e := &reposEnv{t: t, pool: pool, router: router, id: id, git: git}
	e.create("alice", `{"owner":"alice","name":"privato","visibility":"private"}`)
	e.create("alice", `{"owner":"alice","name":"interno","visibility":"internal"}`)
	return &attachEnv{reposEnv: e, dir: dir}
}

// upload manda un multipart con un campo file; user "" = senza credenziali.
func (e *attachEnv) upload(repo, user, field, filename string, content []byte) *httptest.ResponseRecorder {
	e.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="`+field+`"; filename="`+filename+`"`)
	h.Set("Content-Type", "text/html") // il tipo dichiarato non conta
	part, err := mw.CreatePart(h)
	if err != nil {
		e.t.Fatal(err)
	}
	_, _ = part.Write(content)
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/repos/alice/"+repo+"/issue-attachments", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if user != "" {
		trust.Sign(req.Header, trustSecret, trust.Identity{UserID: users[user], Username: user}, time.Now())
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e *attachEnv) uploaded(repo, user, filename string, content []byte) openapi.IssueAttachment {
	e.t.Helper()
	rec := e.upload(repo, user, "file", filename, content)
	e.want(rec, http.StatusCreated)
	var a openapi.IssueAttachment
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		e.t.Fatal(err)
	}
	return a
}

func (e *attachEnv) download(repo, user string, a openapi.IssueAttachment) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/repos/alice/"+repo+"/issue-attachments/"+a.Id.String(), nil)
	if user != "" {
		trust.Sign(req.Header, trustSecret, trust.Identity{UserID: users[user], Username: user}, time.Now())
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e *attachEnv) link(a openapi.IssueAttachment, repo string) {
	e.t.Helper()
	// Collega l'allegato a una issue (lo farà GIT-104): qui direttamente in SQL.
	var repoID uuid.UUID
	if err := e.pool.QueryRow(context.Background(),
		`SELECT resource_id FROM core.repositories WHERE name = $1`, repo).Scan(&repoID); err != nil {
		e.t.Fatal(err)
	}
	issue := uuid.New()
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO core.issues (id, repo_id, number, title, author_id) VALUES ($1, $2, 1, 't', $3)`,
		issue, repoID, uuid.MustParse(aliceID)); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE core.issue_attachments SET issue_id = $1 WHERE id = $2`, issue, a.Id); err != nil {
		e.t.Fatal(err)
	}
}

func TestAttachments_UploadEDownload(t *testing.T) {
	e := newAttachEnv(t)

	a := e.uploaded("interno", "bob", "../../etc/schermata.png", pngBytes)
	if a.ContentType != "image/png" || a.Size != int64(len(pngBytes)) || a.Filename != "schermata.png" {
		t.Errorf("metadati inattesi: %+v", a)
	}
	if a.Url == nil || !strings.HasSuffix(*a.Url, a.Id.String()) {
		t.Errorf("url: %v", a.Url)
	}
	// Il file sta in <repo_id>/<id>, senza il nome originale.
	var found string
	_ = filepath.Walk(e.dir, func(p string, info os.FileInfo, _ error) error {
		if !info.IsDir() {
			found = p
		}
		return nil
	})
	if filepath.Base(found) != a.Id.String() {
		t.Errorf("percorso su disco %q", found)
	}

	// Un allegato non collegato lo scarica solo chi l'ha caricato.
	e.want(e.download("interno", "bob", a), 200)
	e.want(e.download("interno", "alice", a), 404)
	e.link(a, "interno")

	rec := e.download("interno", "alice", a)
	e.want(rec, 200)
	if !bytes.Equal(rec.Body.Bytes(), pngBytes) {
		t.Error("byte diversi dall'originale")
	}
	hd := rec.Header()
	if hd.Get("Content-Type") != "image/png" || !strings.HasPrefix(hd.Get("Content-Disposition"), "attachment") ||
		hd.Get("X-Content-Type-Options") != "nosniff" || hd.Get("Content-Security-Policy") != "sandbox" {
		t.Errorf("intestazioni: %v", hd)
	}
	// Repo interno: bob ha read e scarica.
	e.want(e.download("interno", "bob", a), 200)
	// Senza credenziali: 401.
	e.want(e.download("interno", "", a), 401)

	// Repo privato: chi non ha permesso riceve 404 (non rivela nulla).
	p := e.uploaded("privato", "alice", "log.txt", []byte("riga 1\nriga 2\n"))
	e.link(p, "privato")
	e.want(e.download("privato", "alice", p), 200)
	no := e.download("privato", "bob", p)
	e.want(no, 404)
	if strings.Contains(no.Body.String(), "privato") {
		t.Errorf("il 404 rivela il repo: %s", no.Body.String())
	}
	e.want(e.download("privato", "", p), 401)
	// Un allegato di un altro repo non si scarica dal repo sbagliato.
	e.want(e.download("interno", "alice", p), 404)

	z := e.uploaded("privato", "alice", "a.zip", zipBytes)
	if z.ContentType != "application/zip" {
		t.Errorf("zip: %s", z.ContentType)
	}
	pdf := e.uploaded("privato", "alice", "a.pdf", []byte("%PDF-1.7\n%...."))
	if pdf.ContentType != "application/pdf" {
		t.Errorf("pdf: %s", pdf.ContentType)
	}
}

func TestAttachments_UploadRifiuti(t *testing.T) {
	e := newAttachEnv(t)

	// Senza credenziali: 401. Senza permesso di lettura: 404.
	e.want(e.upload("interno", "", "file", "a.png", pngBytes), 401)
	e.want(e.upload("privato", "bob", "file", "a.png", pngBytes), 404)

	// Oltre il limite: 413, nessun file resta sul volume.
	big := append([]byte("riga di log\n"), bytes.Repeat([]byte("x"), attachMax)...)
	rec := e.upload("interno", "alice", "file", "grande.log", big)
	e.want(rec, 413)
	if !strings.Contains(rec.Body.String(), "attachment_too_large") {
		t.Errorf("codice: %s", rec.Body.String())
	}
	// Esattamente al limite: ammesso.
	e.want(e.upload("interno", "alice", "file", "limite.log", bytes.Repeat([]byte("x"), attachMax)), 201)

	// Tipo non ammesso, dai byte e non dal nome o dal Content-Type dichiarato.
	for name, content := range map[string][]byte{
		"pagina.html":    []byte("<!DOCTYPE html><html><script>alert(1)</script></html>"),
		"travestito.png": []byte("<html><body>ciao</body></html>"),
		"img.svg":        []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"img2.png":       []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>`),
		"spazi.txt":      []byte("\n  <svg onload=alert(1)>"),
		"app.exe":        append([]byte("MZ"), bytes.Repeat([]byte{0, 1, 2, 3}, 20)...),
		"vuoto.txt":      {},
	} {
		rec := e.upload("interno", "alice", "file", name, content)
		if rec.Code != 422 || !strings.Contains(rec.Body.String(), "unsupported_media_type") {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	// Un PNG rinominato .html è comunque un PNG, e si scarica come attachment.
	a := e.uploaded("interno", "alice", "pagina.html", pngBytes)
	e.link(a, "interno")
	rec = e.download("interno", "alice", a)
	if rec.Header().Get("Content-Type") != "image/png" || !strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("png rinominato: %v", rec.Header())
	}

	// Campo sbagliato o corpo non multipart: 400.
	e.want(e.upload("interno", "alice", "altro", "a.png", pngBytes), 400)
	req := httptest.NewRequest(http.MethodPost, "/repos/alice/interno/issue-attachments", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	trust.Sign(req.Header, trustSecret, trust.Identity{UserID: aliceID, Username: "alice"}, time.Now())
	r := httptest.NewRecorder()
	e.router.ServeHTTP(r, req)
	e.want(r, 400)

	// Repo archiviato: 409.
	e.want(e.do("PATCH", "/repos/alice/interno", "alice", `{"archived":true}`), 200)
	e.want(e.upload("interno", "alice", "file", "a.png", pngBytes), 409)

	// Nel DB ci sono solo gli allegati accettati: nessuna riga per i rifiuti.
	if n := e.count(`SELECT count(*) FROM core.issue_attachments`); n != 2 {
		t.Errorf("righe %d, volute 2 (limite.log e pagina.html)", n)
	}
	// E nessun file di troppo (né temporanei) sul volume.
	files := 0
	_ = filepath.Walk(e.dir, func(_ string, info os.FileInfo, _ error) error {
		if !info.IsDir() {
			files++
		}
		return nil
	})
	if files != 2 {
		t.Errorf("file sul volume %d, voluti 2", files)
	}
}

func TestAttachments_NomeFileNelleIntestazioni(t *testing.T) {
	e := newAttachEnv(t)
	a := e.uploaded("interno", "alice", "rè;port.txt", []byte("ciao\n"))
	e.link(a, "interno")
	rec := e.download("interno", "alice", a)
	e.want(rec, 200)
	if rec.Header().Get("X-Evil") != "" || strings.ContainsAny(rec.Header().Get("Content-Disposition"), "\r\n") {
		t.Errorf("iniezione di intestazioni: %v", rec.Header())
	}
	if rec.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Errorf("tipo: %s", rec.Header().Get("Content-Type"))
	}
}

func TestAttachments_IssueNascosta(t *testing.T) {
	e := newAttachEnv(t)
	a := e.uploaded("interno", "bob", "a.png", pngBytes)
	e.link(a, "interno")
	e.want(e.download("interno", "bob", a), 200)
	if _, err := e.pool.Exec(context.Background(), `UPDATE core.issues SET hidden = true`); err != nil {
		t.Fatal(err)
	}
	e.want(e.download("interno", "bob", a), 404) // read, non admin
	e.want(e.download("interno", "alice", a), 200)
}

func TestAttachments_SenzaVolume(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	id := newFakeIdentity()
	router := httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret,
		httpserver.WithRepoIdentity(id), httpserver.WithGit(newFakeGit()))
	e := &attachEnv{reposEnv: &reposEnv{t: t, pool: pool, router: router, id: id, git: newFakeGit()}}
	e.create("alice", `{"owner":"alice","name":"r","visibility":"private"}`)
	e.want(e.upload("r", "alice", "file", "a.png", pngBytes), 503)
}

// filesOnDisk conta i file nel volume degli allegati.
func filesOnDisk(dir string) int {
	n := 0
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, _ error) error {
		if info != nil && !info.IsDir() {
			n++
		}
		return nil
	})
	return n
}

// TestAttachments_QuotaPerRepo: quota di allegati per repo e per utente (F17,
// V12.1.3). pngBytes pesa 56 byte; quota di repo 150 byte, di utente 120.
func TestAttachments_QuotaPerRepo(t *testing.T) {
	e := newAttachEnvQuota(t, 150, 120)
	size := int64(len(pngBytes))
	if size != 56 {
		t.Fatalf("la prova assume pngBytes di 56 byte, sono %d", size)
	}

	// Due allegati stanno nel repo (112 <= 150) e nella quota dell'utente (112 <= 120).
	e.uploaded("privato", "alice", "a.png", pngBytes)
	e.uploaded("privato", "alice", "b.png", pngBytes)

	// Il terzo nello stesso repo supera la quota del repo (168 > 150): 413 e
	// il file non resta sul volume.
	rec := e.upload("privato", "alice", "file", "c.png", pngBytes)
	e.want(rec, http.StatusRequestEntityTooLarge)
	if !strings.Contains(rec.Body.String(), "attachment_quota_exceeded") || !strings.Contains(rec.Body.String(), "per questo repo") {
		t.Errorf("risposta inattesa: %s", rec.Body.String())
	}
	if n := filesOnDisk(e.dir); n != 2 {
		t.Errorf("sul volume ci sono %d file, attesi 2", n)
	}

	// In un altro repo lo stesso utente è fermo dalla quota per utente (168 > 120).
	rec = e.upload("interno", "alice", "file", "d.png", pngBytes)
	e.want(rec, http.StatusRequestEntityTooLarge)
	if !strings.Contains(rec.Body.String(), "attachment_quota_exceeded") || !strings.Contains(rec.Body.String(), "per il tuo utente") {
		t.Errorf("risposta inattesa: %s", rec.Body.String())
	}

	// Un altro utente non è toccato dalla quota di alice, e il repo interno
	// ha ancora spazio.
	e.uploaded("interno", "bob", "e.png", pngBytes)

	// Il file è già su disco quando si conosce la dimensione: un singolo file
	// che da solo supera la quota del repo vuoto è rifiutato e rimosso.
	small := newAttachEnvQuota(t, 40, 0)
	rec = small.upload("privato", "alice", "file", "grande.png", pngBytes)
	small.want(rec, http.StatusRequestEntityTooLarge)
	if n := filesOnDisk(small.dir); n != 0 {
		t.Errorf("il file oltre la quota è rimasto sul volume (%d file)", n)
	}

	// Quota negativa = nessun limite.
	free := newAttachEnvQuota(t, -1, -1)
	for i := 0; i < 5; i++ {
		free.uploaded("privato", "alice", "x.png", pngBytes)
	}
}
