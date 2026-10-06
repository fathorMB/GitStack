// Package issuelinks collega i commit alle issues e chiude le issues con
// `fixes #n` (M-06/C, GIT-131; regole C1 e C2 in
// .prisma/knowledge/topics/collegamenti-notifiche-webhook.md).
//
// Parte dall'evento git.push v1: il consumer durevole "core-issue-linker"
// (internal/gitpush) chiama HandlePush. L'elaborazione di un push è una sola
// transazione e idempotente: il collegamento è una riga unica per (issue, repo
// del commit, sha) e la chiusura si applica una volta sola
// (closed_applied_at), quindi lo stesso evento consegnato due volte, o lo
// stesso commit visto di nuovo su un altro ref, non duplica collegamenti,
// cronologia, notifiche né chiusure.
//
//   - Un commit su qualsiasi branch crea il collegamento («linked commit»).
//   - La chiusura «completata» (I2), con la traccia «closed by commit <sha>», si
//     applica solo quando il commit è nel branch principale (R4), solo se la
//     issue è aperta e archiviata no (R10), solo se chi ha fatto il push ha
//     write sul repo della issue (C1; per lo stesso repo il push al branch
//     principale lo implica) e solo se la issue non è stata riaperta dopo il
//     collegamento.
//   - owner/repo#n: chi fa il push deve leggere il repo della issue, altrimenti
//     non si crea niente (non si sonda un repo che non si vede). Il collegamento
//     e la chiusura compaiono nella cronologia solo a chi vede anche il repo del
//     commit (filtro in ListIssueEvents), e le notifiche vanno solo a chi legge
//     entrambi i repo.
package issuelinks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	pkgevents "github.com/fathorMB/GitStack/pkg/events"
	pkggitpush "github.com/fathorMB/GitStack/pkg/events/gitpush"
	"github.com/fathorMB/GitStack/services/core/internal/domainevents"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/notify"
	"github.com/fathorMB/GitStack/services/core/internal/outbox"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Identity è ciò che serve a identity: il ruolo effettivo di un utente su un repo.
type Identity interface {
	HasRole(ctx context.Context, userID, resourceID uuid.UUID, role string) (bool, error)
}

// Notifier scrive notifiche nella transazione data (notify.Engine).
type Notifier interface {
	Notify(ctx context.Context, tx pgx.Tx, reason string, recipients []uuid.UUID, o notify.OccurrenceInput) error
}

// MaxRangeCommits è il tetto dei commit ricostruiti da before..after quando
// l'elenco dell'evento è troncato.
const MaxRangeCommits = 2000

// EventCommitLinked è il nome con cui la notifica ricorda l'evento.
const EventCommitLinked = "issue.commit_linked"

// Linker elabora i git.push.
type Linker struct {
	Pool     *pgxpool.Pool
	Identity Identity
	// Notifier: nil = nessuna notifica (identity non configurata).
	Notifier Notifier
	// Git serve a ricostruire before..after se l'elenco è troncato; nil = ci si
	// accontenta dei commit dell'evento (con un avviso).
	Git gitclient.Reader
	Log *slog.Logger
	// Now è l'orologio (iniettabile); default time.Now.
	Now func() time.Time
}

func (l *Linker) log() *slog.Logger {
	if l.Log != nil {
		return l.Log
	}
	return slog.Default()
}

// commitItem è un commit da elaborare, con il ref in cui è stato visto per
// primo e se è (anche) nel branch principale.
type commitItem struct {
	c         pkggitpush.Commit
	ref       string
	onDefault bool
}

type repoInfo struct {
	id       uuid.UUID
	fullName string // owner/repo come in core
	archived bool
}

// target è un repo citato dai commit, con i permessi di chi ha fatto il push.
type target struct {
	repoInfo
	found     bool
	canRead   bool
	writeKnow bool
	canWrite  bool
}

// HandlePush è il gitpush.Handler del collegamento commit↔issue.
func (l *Linker) HandlePush(ctx context.Context, _ pkgevents.Envelope, p pkggitpush.Payload) error {
	repoID, err := uuid.Parse(p.Repo.ID)
	if err != nil {
		l.log().Error("git.push con repo.id non valido, scartato", "id", p.Repo.ID)
		return nil
	}
	pusher, err := uuid.Parse(p.Pusher.ID)
	if err != nil {
		l.log().Error("git.push con pusher.id non valido, scartato", "id", p.Pusher.ID)
		return nil
	}
	items, err := l.collect(ctx, repoID, pusher, p)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}

	tx, err := l.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var cur repoInfo
	var deleted bool
	err = tx.QueryRow(ctx, `SELECT resource_id, owner_name || '/' || name, archived_at IS NOT NULL, deleted_at IS NOT NULL
		FROM core.repositories WHERE resource_id = $1`, repoID).Scan(&cur.id, &cur.fullName, &cur.archived, &deleted)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && deleted) {
		return nil // repo eliminato: niente da collegare
	}
	if err != nil {
		return err
	}
	run := &pushRun{l: l, tx: tx, cur: cur, pusher: pusher, targets: map[string]*target{}}
	for _, it := range items {
		for _, ref := range ParseRefs(it.c.Message) {
			if err := run.apply(ctx, it, ref); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

// collect elenca i commit dei branch aggiornati (senza duplicati), ricostruendo
// before..after quando l'elenco di un ref è troncato (letture di M-04).
func (l *Linker) collect(ctx context.Context, repoID, pusher uuid.UUID, p pkggitpush.Payload) ([]commitItem, error) {
	var out []commitItem
	index := map[string]int{}
	for _, rp := range p.Refs {
		if !strings.HasPrefix(rp.Ref, "refs/heads/") || rp.After == pkggitpush.ZeroSHA || rp.After == "" {
			continue // tag e branch eliminati: nessun commit nuovo da collegare
		}
		commits := rp.Commits
		if rp.CommitsTruncated {
			if l.Git == nil {
				l.log().Warn("git.push troncato e nessuna lettura di git: si collegano solo i commit dell'evento", "repo", p.Repo.FullName, "ref", rp.Ref)
			} else {
				full, err := l.rangeCommits(ctx, repoID, pusher, p.Pusher.Username, rp)
				if err != nil {
					return nil, fmt.Errorf("ricostruzione di %s..%s: %w", rp.Before, rp.After, err)
				}
				commits = full
			}
		}
		for _, c := range commits {
			if i, ok := index[c.SHA]; ok {
				out[i].onDefault = out[i].onDefault || rp.IsDefaultBranch
				continue
			}
			index[c.SHA] = len(out)
			out = append(out, commitItem{c: c, ref: rp.Ref, onDefault: rp.IsDefaultBranch})
		}
	}
	return out, nil
}

// rangeCommits ricostruisce l'intervallo before..after leggendo lo storico di
// After a pagine (GET commits del servizio git) finché trova Before. Con un
// ref creato (Before a zero) si ferma al tetto MaxRangeCommits.
func (l *Linker) rangeCommits(ctx context.Context, repoID, pusher uuid.UUID, username string, rp pkggitpush.RefPush) ([]pkggitpush.Commit, error) {
	caller := trust.Identity{UserID: pusher.String(), Username: username}
	var out []pkggitpush.Commit
	for page := 1; len(out) < MaxRangeCommits; page++ {
		raw, err := l.Git.ReadJSON(ctx, caller, repoID, "commits", url.Values{
			"ref": {rp.After}, "page": {strconv.Itoa(page)}, "perPage": {"100"},
		})
		if err != nil {
			return nil, err
		}
		var list struct {
			Items []struct {
				SHA       string `json:"sha"`
				Message   string `json:"message"`
				Subject   string `json:"subject"`
				Author    pkggitpush.Person
				Committer pkggitpush.Person
			} `json:"items"`
			HasMore bool `json:"hasMore"`
		}
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, err
		}
		for _, it := range list.Items {
			if it.SHA == rp.Before {
				return out, nil
			}
			msg := it.Message
			if msg == "" {
				msg = it.Subject
			}
			out = append(out, pkggitpush.Commit{SHA: it.SHA, Author: it.Author, Committer: it.Committer, Message: msg})
		}
		if !list.HasMore {
			break
		}
	}
	return out, nil
}

type pushRun struct {
	l       *Linker
	tx      pgx.Tx
	cur     repoInfo
	pusher  uuid.UUID
	targets map[string]*target
}

// resolve trova il repo citato (vuoto = quello del commit) e se chi ha fatto
// il push lo legge.
func (r *pushRun) resolve(ctx context.Context, repo string) (*target, error) {
	key := repo
	if repo == "" || repo == strings.ToLower(r.cur.fullName) {
		key = ""
	}
	if t, ok := r.targets[key]; ok {
		return t, nil
	}
	t := &target{}
	r.targets[key] = t
	if key == "" {
		t.repoInfo, t.found, t.canRead, t.canWrite, t.writeKnow = r.cur, true, true, true, true
		return t, nil
	}
	owner, name, ok := strings.Cut(key, "/")
	if !ok {
		return t, nil
	}
	var deleted bool
	err := r.tx.QueryRow(ctx, `SELECT resource_id, owner_name || '/' || name, archived_at IS NOT NULL, deleted_at IS NOT NULL
		FROM core.repositories WHERE lower(owner_name) = $1 AND lower(name) = $2`, owner, name).
		Scan(&t.id, &t.fullName, &t.archived, &deleted)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && deleted) {
		return t, nil
	}
	if err != nil {
		return nil, err
	}
	t.found = true
	if t.canRead, err = r.l.Identity.HasRole(ctx, r.pusher, t.id, "read"); err != nil {
		return nil, fmt.Errorf("verifica del permesso read: %w", err)
	}
	return t, nil
}

// write dice se chi ha fatto il push ha write sul repo (C1).
func (r *pushRun) write(ctx context.Context, t *target) (bool, error) {
	if !t.writeKnow {
		ok, err := r.l.Identity.HasRole(ctx, r.pusher, t.id, "write")
		if err != nil {
			return false, fmt.Errorf("verifica del permesso write: %w", err)
		}
		t.canWrite, t.writeKnow = ok, true
	}
	return t.canWrite, nil
}

type commitData struct {
	SHA          string `json:"sha"`
	Repository   string `json:"repository"`
	RepositoryID string `json:"repositoryId"`
	Subject      string `json:"subject"`
	Ref          string `json:"ref"`
	AuthorName   string `json:"authorName"`
}

func subjectOf(msg string) string {
	s, _, _ := strings.Cut(strings.TrimSpace(msg), "\n")
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 512 {
		s = string(r[:512])
	}
	return s
}

// apply collega (ed eventualmente chiude) la issue citata da un commit.
func (r *pushRun) apply(ctx context.Context, it commitItem, ref Ref) error {
	t, err := r.resolve(ctx, ref.Repo)
	if err != nil {
		return err
	}
	if !t.found || !t.canRead {
		return nil
	}
	var (
		issueID uuid.UUID
		state   string
		hidden  bool
		title   string
	)
	err = r.tx.QueryRow(ctx, `SELECT id, state, hidden, title FROM core.issues WHERE repo_id = $1 AND number = $2 FOR UPDATE`,
		t.id, ref.Number).Scan(&issueID, &state, &hidden, &title)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // numero di una PR o inesistente
	}
	if err != nil {
		return err
	}
	if hidden {
		// Una issue nascosta non esiste per chi non è admin (I4).
		if ok, err := r.l.Identity.HasRole(ctx, r.pusher, t.id, "admin"); err != nil {
			return fmt.Errorf("verifica del permesso admin: %w", err)
		} else if !ok {
			return nil
		}
	}

	sha := strings.ToLower(it.c.SHA)
	var kw any
	if ref.Keyword != "" {
		kw = ref.Keyword
	}
	var committedAt any
	if d, err := time.Parse(time.RFC3339, it.c.Committer.Date); err == nil {
		committedAt = d
	}
	var linkID uuid.UUID
	var closedApplied *time.Time
	var linkCreated time.Time
	err = r.tx.QueryRow(ctx, `INSERT INTO core.issue_commit_links
			(id, issue_id, commit_repo_id, commit_sha, ref, subject, author_name, committed_at, pusher_id, close_keyword, on_default_branch, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, clock_timestamp())
		ON CONFLICT (issue_id, commit_repo_id, commit_sha) DO NOTHING
		RETURNING id, created_at`,
		uuid.New(), issueID, r.cur.id, sha, it.ref, subjectOf(it.c.Message), it.c.Author.Name, committedAt, r.pusher, kw, it.onDefault).
		Scan(&linkID, &linkCreated)
	created := err == nil
	if errors.Is(err, pgx.ErrNoRows) {
		// Già collegato: lo stesso evento riconsegnato, o lo stesso commit su un
		// altro ref. Se ora è nel branch principale lo si segna.
		err = r.tx.QueryRow(ctx, `UPDATE core.issue_commit_links
			SET on_default_branch = on_default_branch OR $4,
			    close_keyword = COALESCE(close_keyword, $5)
			WHERE issue_id = $1 AND commit_repo_id = $2 AND commit_sha = $3
			RETURNING id, created_at, closed_applied_at`, issueID, r.cur.id, sha, it.onDefault, kw).
			Scan(&linkID, &linkCreated, &closedApplied)
	}
	if err != nil {
		return fmt.Errorf("collegamento del commit %s: %w", sha, err)
	}

	cd := commitData{SHA: sha, Repository: r.cur.fullName, RepositoryID: r.cur.id.String(),
		Subject: subjectOf(it.c.Message), Ref: it.ref, AuthorName: it.c.Author.Name}
	if created {
		if err := r.insertEvent(ctx, issueID, "commit_linked", &r.pusher, map[string]any{"commit": cd}); err != nil {
			return err
		}
		if err := r.notifyLinked(ctx, t, issueID, state, title, hidden, cd); err != nil {
			return err
		}
	}

	if !it.onDefault || ref.Keyword == "" || closedApplied != nil {
		return nil
	}
	return r.closeIssue(ctx, t, issueID, state, linkID, linkCreated, cd)
}

// closeIssue chiude la issue come completata per il commit entrato nel
// branch principale.
func (r *pushRun) closeIssue(ctx context.Context, t *target, issueID uuid.UUID, state string, linkID uuid.UUID, linkCreated time.Time, cd commitData) error {
	if t.archived {
		return nil // R10: sola lettura, resta il collegamento
	}
	if t.id != r.cur.id {
		ok, err := r.write(ctx, t)
		if err != nil {
			return err
		}
		if !ok {
			return nil // C1: senza write sul repo della issue, solo il collegamento
		}
	}
	// Riaperta dopo il collegamento: lo stesso commit non la richiude.
	var reopened bool
	if err := r.tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM core.issue_events WHERE issue_id = $1 AND type = 'reopened' AND created_at > $2)`,
		issueID, linkCreated).Scan(&reopened); err != nil {
		return err
	}
	if reopened {
		return nil
	}
	if _, err := r.tx.Exec(ctx, `UPDATE core.issue_commit_links SET closed_applied_at = clock_timestamp() WHERE id = $1`, linkID); err != nil {
		return err
	}
	if state != "open" {
		return nil // già chiusa da altri: niente da fare, e il commit non la richiuderà dopo una riapertura
	}
	if _, err := r.tx.Exec(ctx, `UPDATE core.issues SET state = 'closed', close_reason = 'completed', duplicate_of = NULL,
		closed_at = now(), updated_at = now() WHERE id = $1`, issueID); err != nil {
		return fmt.Errorf("chiusura della issue: %w", err)
	}
	// Per una chiusura da commit la cronologia ha solo closed_by_commit, senza
	// actor e senza un «closed» in più.
	if err := r.insertEvent(ctx, issueID, "closed_by_commit", nil, map[string]any{"commit": cd, "reason": "completed"}); err != nil {
		return err
	}
	return r.emitClosed(ctx, t, issueID, cd)
}

func (r *pushRun) insertEvent(ctx context.Context, issueID uuid.UUID, typ string, actor *uuid.UUID, data map[string]any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = r.tx.Exec(ctx, `INSERT INTO core.issue_events (id, issue_id, type, actor_id, data, created_at)
		VALUES ($1, $2, $3, $4, $5, clock_timestamp())`, uuid.New(), issueID, typ, actor, b)
	if err != nil {
		return fmt.Errorf("scrittura dell'evento %s: %w", typ, err)
	}
	return nil
}

// emitClosed accoda issue.closed (con commit, actor null) nell'outbox: da lì
// partono notifiche (state_change) e webhook, come per ogni chiusura.
func (r *pushRun) emitClosed(ctx context.Context, t *target, issueID uuid.UUID, cd commitData) error {
	var repo domainevents.Repo
	var iss domainevents.Issue
	var repoID, authorID, id uuid.UUID
	var owner, name string
	err := r.tx.QueryRow(ctx, `SELECT r.resource_id, r.owner_name, r.name, r.default_branch, r.visibility, r.archived_at IS NOT NULL,
			i.id, i.number, i.title, i.state, i.author_id, i.hidden, i.locked
		FROM core.issues i JOIN core.repositories r ON r.resource_id = i.repo_id WHERE i.id = $1`, issueID).
		Scan(&repoID, &owner, &name, &repo.DefaultBranch, &repo.Visibility, &repo.Archived,
			&id, &iss.Number, &iss.Title, &iss.State, &authorID, &iss.Hidden, &iss.Locked)
	if err != nil {
		return fmt.Errorf("istantanea della issue per l'evento: %w", err)
	}
	repo.ID, repo.FullName = repoID.String(), owner+"/"+name
	iss.ID, iss.AuthorID, iss.AssigneeIDs = id.String(), authorID.String(), []string{}
	rows, err := r.tx.Query(ctx, `SELECT user_id FROM core.issue_assignees WHERE issue_id = $1 ORDER BY created_at, user_id`, issueID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var u uuid.UUID
		if err := rows.Scan(&u); err != nil {
			rows.Close()
			return err
		}
		iss.AssigneeIDs = append(iss.AssigneeIDs, u.String())
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	p := domainevents.IssuePayload{Repo: repo, Issue: iss, Reason: "completed"}
	// Il commit di un altro repo non entra nel payload: webhook e consumer del
	// repo della issue non devono conoscere un repo che i loro lettori
	// potrebbero non vedere.
	if t.id == r.cur.id {
		p.Commit = &domainevents.Commit{SHA: cd.SHA, Repository: cd.Repository}
	}
	return outbox.Enqueue(ctx, r.tx, domainevents.IssueClosed, domainevents.Version, p)
}

// notifyLinked avvisa chi segue la issue (C3: commit collegati), mai chi ha
// fatto il push; se il commit è di un altro repo, solo chi legge anche quello.
func (r *pushRun) notifyLinked(ctx context.Context, t *target, issueID uuid.UUID, state, title string, hidden bool, cd commitData) error {
	if r.l.Notifier == nil {
		return nil
	}
	rows, err := r.tx.Query(ctx, `SELECT user_id FROM core.issue_subscriptions WHERE issue_id = $1 AND subscribed ORDER BY user_id`, issueID)
	if err != nil {
		return err
	}
	var recipients []uuid.UUID
	for rows.Next() {
		var u uuid.UUID
		if err := rows.Scan(&u); err != nil {
			rows.Close()
			return err
		}
		recipients = append(recipients, u)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if t.id != r.cur.id {
		kept := recipients[:0]
		for _, u := range recipients {
			ok, err := r.l.Identity.HasRole(ctx, u, r.cur.id, "read")
			if err != nil {
				return fmt.Errorf("verifica dell'accesso al repo del commit: %w", err)
			}
			if ok {
				kept = append(kept, u)
			}
		}
		recipients = kept
	}
	if len(recipients) == 0 {
		return nil
	}
	var number int64
	if err := r.tx.QueryRow(ctx, `SELECT number FROM core.issues WHERE id = $1`, issueID).Scan(&number); err != nil {
		return err
	}
	return r.l.Notifier.Notify(ctx, r.tx, notify.ReasonCommitLinked, recipients, notify.OccurrenceInput{
		EventName: EventCommitLinked, RepoID: t.id, IssueID: issueID, Actor: r.pusher, Hidden: hidden,
		Data: map[string]any{
			"number": number, "title": title, "state": state,
			"commit": map[string]any{"sha": cd.SHA, "repository": cd.Repository, "subject": cd.Subject},
		},
	})
}
