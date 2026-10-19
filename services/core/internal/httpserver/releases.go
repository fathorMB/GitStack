package httpserver

// Release REL1 (GIT-289). I metadati sono nel database, perciò entrano nel
// backup; gli asset usano il volume degli allegati e restano fuori dal dump.

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
)

const maxReleaseAssetBytes int64 = 2 << 30

type releaseRow struct {
	id                uuid.UUID
	tag, name, body   string
	draft, prerelease bool
	createdAt         time.Time
	publishedAt       *time.Time
}

// generatedReleaseNotes usa il confronto Git fra l'ultima release pubblicata
// e il tag richiesto. Le PR unite del repo completano la bozza: il corpo resta
// modificabile prima della pubblicazione.
func (s *apiServer) generatedReleaseNotes(ctx context.Context, ia issueAccess, tag string) (string, error) {
	var base string
	_ = s.pool.QueryRow(ctx, `SELECT tag_name FROM core.releases WHERE repo_id=$1 AND NOT draft ORDER BY published_at DESC NULLS LAST LIMIT 1`, ia.repo.ID).Scan(&base)
	var b strings.Builder
	b.WriteString("## Cambiamenti\n")
	if base != "" {
		rd, ok := s.git.(gitclient.Reader)
		if !ok {
			return "", gitclient.ErrUnavailable
		}
		raw, err := rd.ReadJSON(ctx, ia.caller, ia.repo.ID, "compare", url.Values{"base": {base}, "head": {tag}})
		if err != nil {
			return "", err
		}
		var cmp struct {
			Commits []openapi.CommitSummary `json:"commits"`
		}
		if err := json.Unmarshal(raw, &cmp); err != nil {
			return "", err
		}
		for _, c := range cmp.Commits {
			b.WriteString("- ")
			b.WriteString(c.Subject)
			b.WriteString(" (`")
			if len(c.Sha) > 7 {
				b.WriteString(c.Sha[:7])
			} else {
				b.WriteString(c.Sha)
			}
			b.WriteString("`)\n")
		}
	} else {
		b.WriteString("\nPrima release del repository.\n")
	}
	rows, err := s.pool.Query(ctx, `SELECT number,title FROM core.pull_requests WHERE repo_id=$1 AND state='merged' ORDER BY merged_at`, ia.repo.ID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var prs []string
	for rows.Next() {
		var n int64
		var title string
		if err := rows.Scan(&n, &title); err != nil {
			return "", err
		}
		prs = append(prs, "- #"+strconv.FormatInt(n, 10)+" "+title)
	}
	if len(prs) > 0 {
		b.WriteString("\n## Pull Request\n")
		b.WriteString(strings.Join(prs, "\n"))
		b.WriteByte('\n')
	}
	return b.String(), rows.Err()
}

func releaseAccess(w http.ResponseWriter, r *http.Request, s *apiServer, o, n string, write bool) (issueAccess, bool) {
	ia, ok := s.issueAccessFor(w, r, o, n)
	if !ok {
		return ia, false
	}
	if write && (!ia.write || ia.rejectArchived(w)) {
		if !ia.write {
			writeError(w, http.StatusNotFound, "not_found", "Release non trovata.")
		}
		return ia, false
	}
	return ia, true
}

func scanRelease(row interface{ Scan(...any) error }) (releaseRow, error) {
	var x releaseRow
	err := row.Scan(&x.id, &x.tag, &x.name, &x.body, &x.draft, &x.prerelease, &x.createdAt, &x.publishedAt)
	return x, err
}
func (s *apiServer) apiRelease(ctx context.Context, x releaseRow) (openapi.Release, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, sha256, size_bytes FROM core.release_assets WHERE release_id=$1 ORDER BY created_at, id`, x.id)
	if err != nil {
		return openapi.Release{}, err
	}
	defer rows.Close()
	assets := []openapi.ReleaseAsset{}
	for rows.Next() {
		var a openapi.ReleaseAsset
		if err := rows.Scan(&a.Id, &a.Name, &a.Sha256, &a.SizeBytes); err != nil {
			return openapi.Release{}, err
		}
		assets = append(assets, a)
	}
	if err := rows.Err(); err != nil {
		return openapi.Release{}, err
	}
	latest := !x.draft && !x.prerelease && x.publishedAt != nil
	if latest {
		err = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.releases WHERE repo_id=(SELECT repo_id FROM core.releases WHERE id=$1) AND id<>$1 AND NOT draft AND NOT prerelease AND published_at > $2)`, x.id, x.publishedAt).Scan(&latest)
		latest = !latest
	}
	return openapi.Release{Id: x.id, TagName: x.tag, Name: x.name, Body: x.body, Draft: x.draft, Prerelease: x.prerelease, CreatedAt: x.createdAt, PublishedAt: x.publishedAt, Latest: latest, Assets: assets}, err
}
func decodeRelease(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "Corpo JSON non valido.")
		return false
	}
	return true
}

// emitReleaseWebhook mette la consegna nella coda già firmata dal motore
// webhooks. I file non sono nel payload: la release contiene solo metadati e
// digest, coerentemente con il backup che esclude il volume degli asset.
func (s *apiServer) emitReleaseWebhook(ctx context.Context, ia issueAccess, rel openapi.Release, action string) {
	payload, err := json.Marshal(map[string]any{
		"version": 1, "event": "release", "action": action, "time": s.now().UTC().Format(time.RFC3339),
		"repository": map[string]any{"id": ia.repo.ID.String(), "fullName": ia.repo.OwnerName + "/" + ia.repo.Name, "owner": ia.repo.OwnerName, "name": ia.repo.Name, "defaultBranch": ia.repo.DefaultBranch, "visibility": ia.repo.Visibility, "archived": ia.repo.ArchivedAt != nil},
		"release":    rel,
	})
	if err != nil {
		return
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO core.webhook_deliveries (id,webhook_id,event,action,status,next_attempt_at,payload,created_at)
		SELECT gen_random_uuid(), id, 'release', $2, 'pending', $3, $4, $3 FROM core.webhooks
		WHERE repo_id=$1 AND active AND 'release'=ANY(events)`, ia.repo.ID, action, s.now(), payload)
	if err != nil {
		slog.Default().Warn("accodamento webhook release non riuscito", "err", err)
	}
}

func (s *apiServer) ListReleases(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam) {
	ia, ok := releaseAccess(w, r, s, string(o), string(n), false)
	if !ok {
		return
	}
	rows, err := s.pool.Query(r.Context(), `SELECT id,tag_name,name,body,draft,prerelease,created_at,published_at FROM core.releases WHERE repo_id=$1 ORDER BY created_at DESC`, ia.repo.ID)
	if err != nil {
		writeError(w, 500, "internal_error", "Lettura delle release non riuscita.")
		return
	}
	defer rows.Close()
	out := openapi.ReleaseList{Items: []openapi.Release{}}
	for rows.Next() {
		x, e := scanRelease(rows)
		if e != nil {
			writeError(w, 500, "internal_error", "Lettura delle release non riuscita.")
			return
		}
		a, e := s.apiRelease(r.Context(), x)
		if e != nil {
			writeError(w, 500, "internal_error", "Lettura delle release non riuscita.")
			return
		}
		out.Items = append(out.Items, a)
	}
	writeJSON(w, 200, out)
}
func (s *apiServer) CreateRelease(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam) {
	ia, ok := releaseAccess(w, r, s, string(o), string(n), true)
	if !ok {
		return
	}
	var in openapi.CreateReleaseInput
	if !decodeRelease(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.TagName) == "" || strings.TrimSpace(in.Name) == "" {
		writeError(w, 400, "invalid_body", "tagName e name sono obbligatori.")
		return
	}
	draft := in.Draft != nil && *in.Draft
	pre := in.Prerelease != nil && *in.Prerelease
	body := ""
	if in.Body != nil {
		body = *in.Body
	}
	if in.GenerateNotes != nil && *in.GenerateNotes && body == "" {
		var err error
		body, err = s.generatedReleaseNotes(r.Context(), ia, in.TagName)
		if err != nil {
			writeError(w, 502, "git_unavailable", "Impossibile generare le note della release.")
			return
		}
	}
	id := uuid.New()
	now := s.now()
	var pub *time.Time
	if !draft {
		pub = &now
	}
	x, err := scanRelease(s.pool.QueryRow(r.Context(), `INSERT INTO core.releases(id,repo_id,tag_name,name,body,draft,prerelease,created_by,created_at,published_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id,tag_name,name,body,draft,prerelease,created_at,published_at`, id, ia.repo.ID, in.TagName, in.Name, body, draft, pre, ia.userID, now, pub))
	if err != nil {
		writeError(w, 409, "conflict", "Esiste già una release per questo tag.")
		return
	}
	out, _ := s.apiRelease(r.Context(), x)
	s.emitReleaseWebhook(r.Context(), ia, out, "created")
	writeJSON(w, 201, out)
}
func (s *apiServer) GetRelease(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, id openapi.ReleaseIdParam) {
	ia, ok := releaseAccess(w, r, s, string(o), string(n), false)
	if !ok {
		return
	}
	x, err := scanRelease(s.pool.QueryRow(r.Context(), `SELECT id,tag_name,name,body,draft,prerelease,created_at,published_at FROM core.releases WHERE id=$1 AND repo_id=$2`, uuid.UUID(id), ia.repo.ID))
	if err != nil {
		writeError(w, 404, "not_found", "Release non trovata.")
		return
	}
	out, err := s.apiRelease(r.Context(), x)
	if err != nil {
		writeError(w, 500, "internal_error", "Lettura della release non riuscita.")
		return
	}
	writeJSON(w, 200, out)
}
func (s *apiServer) UpdateRelease(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, id openapi.ReleaseIdParam) {
	ia, ok := releaseAccess(w, r, s, string(o), string(n), true)
	if !ok {
		return
	}
	var in openapi.UpdateReleaseInput
	if !decodeRelease(w, r, &in) {
		return
	}
	x, err := scanRelease(s.pool.QueryRow(r.Context(), `UPDATE core.releases SET name=COALESCE($3,name),body=COALESCE($4,body),draft=COALESCE($5,draft),prerelease=COALESCE($6,prerelease),published_at=CASE WHEN COALESCE($5,draft) THEN NULL WHEN published_at IS NULL THEN $7 ELSE published_at END WHERE id=$1 AND repo_id=$2 RETURNING id,tag_name,name,body,draft,prerelease,created_at,published_at`, uuid.UUID(id), ia.repo.ID, in.Name, in.Body, in.Draft, in.Prerelease, s.now()))
	if err != nil {
		writeError(w, 404, "not_found", "Release non trovata.")
		return
	}
	out, _ := s.apiRelease(r.Context(), x)
	action := "edited"
	if !out.Draft && out.PublishedAt != nil {
		action = "published"
	}
	s.emitReleaseWebhook(r.Context(), ia, out, action)
	writeJSON(w, 200, out)
}
func (s *apiServer) DeleteRelease(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, id openapi.ReleaseIdParam) {
	ia, ok := releaseAccess(w, r, s, string(o), string(n), true)
	if !ok {
		return
	}
	x, xerr := scanRelease(s.pool.QueryRow(r.Context(), `SELECT id,tag_name,name,body,draft,prerelease,created_at,published_at FROM core.releases WHERE id=$1 AND repo_id=$2`, uuid.UUID(id), ia.repo.ID))
	if xerr != nil {
		writeError(w, 404, "not_found", "Release non trovata.")
		return
	}
	out, _ := s.apiRelease(r.Context(), x)
	tag, err := s.pool.Exec(r.Context(), `DELETE FROM core.releases WHERE id=$1 AND repo_id=$2`, uuid.UUID(id), ia.repo.ID)
	if err != nil {
		writeError(w, 500, "internal_error", "Eliminazione della release non riuscita.")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, 404, "not_found", "Release non trovata.")
		return
	}
	s.emitReleaseWebhook(r.Context(), ia, out, "deleted")
	w.WriteHeader(204)
}

func (s *apiServer) UploadReleaseAsset(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, id openapi.ReleaseIdParam, p openapi.UploadReleaseAssetParams) {
	ia, ok := releaseAccess(w, r, s, string(o), string(n), true)
	if !ok {
		return
	}
	if s.attachments.Disk == nil {
		writeError(w, 503, "attachments_unavailable", "Volume degli allegati non configurato.")
		return
	}
	if strings.TrimSpace(p.Name) == "" {
		writeError(w, 400, "invalid_name", "name è obbligatorio.")
		return
	}
	var exists bool
	if err := s.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM core.releases WHERE id=$1 AND repo_id=$2)`, uuid.UUID(id), ia.repo.ID).Scan(&exists); err != nil || !exists {
		writeError(w, 404, "not_found", "Release non trovata.")
		return
	}
	if r.ContentLength > maxReleaseAssetBytes {
		writeError(w, 413, "asset_too_large", "Allegato oltre 2 GB.")
		return
	}
	aid := uuid.New()
	size, sum, err := s.attachments.Disk.SaveRaw(ia.repo.ID, aid, http.MaxBytesReader(w, r.Body, maxReleaseAssetBytes), maxReleaseAssetBytes)
	if err != nil {
		writeError(w, 422, "invalid_asset", "Impossibile salvare l'allegato.")
		return
	}
	_, err = s.pool.Exec(r.Context(), `INSERT INTO core.release_assets(id,release_id,name,sha256,size_bytes,created_at) VALUES($1,$2,$3,$4,$5,$6)`, aid, uuid.UUID(id), attachmentFilename(p.Name), sum, size, s.now())
	if err != nil {
		_ = s.attachments.Disk.Remove(ia.repo.ID, aid)
		writeError(w, 409, "conflict", "Esiste già un allegato con questo nome.")
		return
	}
	writeJSON(w, 201, openapi.ReleaseAsset{Id: aid, Name: attachmentFilename(p.Name), Sha256: sum, SizeBytes: size})
}
func (s *apiServer) DownloadReleaseAsset(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, id openapi.ReleaseIdParam, aid openapi.AssetIdParam) {
	ia, ok := releaseAccess(w, r, s, string(o), string(n), false)
	if !ok {
		return
	}
	var name, ctype string
	var size int64
	err := s.pool.QueryRow(r.Context(), `SELECT a.name,a.size_bytes FROM core.release_assets a JOIN core.releases x ON x.id=a.release_id WHERE a.id=$1 AND a.release_id=$2 AND x.repo_id=$3`, uuid.UUID(aid), uuid.UUID(id), ia.repo.ID).Scan(&name, &size)
	if err != nil {
		writeError(w, 404, "not_found", "Allegato non trovato.")
		return
	}
	f, err := s.attachments.Disk.Open(ia.repo.ID, uuid.UUID(aid))
	if err != nil {
		writeError(w, 404, "not_found", "Allegato non trovato.")
		return
	}
	defer f.Close()
	ctype = "application/octet-stream"
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(200)
	_, _ = io.Copy(w, f)
}
