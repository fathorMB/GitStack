// Package upstream contiene i client del servizio git verso identity
// (/internal/verify, /internal/permissions/check, Bearer con il segreto di
// servizio) e verso core (GET /repos/{owner}/{repo} con l'identità firmata
// dell'utente, che applica da sé la lettura). Il segreto non finisce mai in
// un messaggio d'errore.
package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/services/git/internal/access"
	"github.com/fathorMB/GitStack/services/git/internal/registry"
	"github.com/fathorMB/GitStack/services/git/internal/trust"
)

// Client chiama identity e core.
type Client struct {
	identity string
	core     string
	secret   string
	// ciGateway è l'URL interno del ci-gateway di core (token del job, registry).
	ciGateway string
	http      *http.Client
	now       func() time.Time
}

// New crea il client. Le due basi sono URL interni dei Service.
func New(identityURL, coreURL, secret string, timeout time.Duration) *Client {
	return &Client{
		identity: strings.TrimRight(identityURL, "/"),
		core:     strings.TrimRight(coreURL, "/"),
		secret:   trust.SigningKey(secret),
		http:     &http.Client{Timeout: timeout},
		now:      time.Now,
	}
}

var (
	_ access.Identity = (*Client)(nil)
	_ access.Core     = (*Client)(nil)
	_ access.Keys     = (*Client)(nil)
)

func sanitize(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}

// VerifyToken implementa access.Identity: solo i token personali (kind
// token); una sessione web non apre l'accesso git.
func (c *Client) VerifyToken(ctx context.Context, token string) (access.Principal, bool, error) {
	if token == "" || len(token) > 512 {
		return access.Principal{}, false, nil
	}
	body, _ := json.Marshal(map[string]string{"credential": token, "kind": "token"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.identity+"/internal/verify", bytes.NewReader(body))
	if err != nil {
		return access.Principal{}, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.secret)
	resp, err := c.http.Do(req)
	if err != nil {
		return access.Principal{}, false, errors.New(sanitize(err))
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return access.Principal{}, false, fmt.Errorf("/internal/verify ha risposto %d", resp.StatusCode)
	}
	var out struct {
		Active    bool `json:"active"`
		Principal *struct {
			UserID     string   `json:"userId"`
			Username   string   `json:"username"`
			AuthMethod string   `json:"authMethod"`
			Kind       string   `json:"kind"`
			Scopes     []string `json:"scopes"`
		} `json:"principal"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return access.Principal{}, false, errors.New("risposta di verify non valida")
	}
	if !out.Active {
		return access.Principal{}, false, nil
	}
	if out.Principal == nil || out.Principal.UserID == "" {
		return access.Principal{}, false, errors.New("verify: active senza principal")
	}
	if out.Principal.AuthMethod != "token" {
		return access.Principal{}, false, nil
	}
	return access.Principal{UserID: out.Principal.UserID, Username: out.Principal.Username, Kind: out.Principal.Kind, Scopes: out.Principal.Scopes}, true, nil
}

// HasRole implementa access.Identity.
func (c *Client) HasRole(ctx context.Context, userID, resourceID, role string) (bool, error) {
	body, _ := json.Marshal(map[string]string{"userId": userID, "resourceId": resourceID, "role": role})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.identity+"/internal/permissions/check", bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.secret)
	resp, err := c.http.Do(req)
	if err != nil {
		return false, errors.New(sanitize(err))
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("permissions/check ha risposto %d", resp.StatusCode)
	}
	var out struct {
		Allowed *bool `json:"allowed"`
	}
	if err := json.Unmarshal(data, &out); err != nil || out.Allowed == nil {
		return false, errors.New("risposta di permissions/check non valida")
	}
	return *out.Allowed, nil
}

// ResolveRepo implementa access.Core: GET /repos/{owner}/{repo} firmato con
// l'identità dell'utente. 404 = inesistente, eliminato o non leggibile.
func (c *Client) ResolveRepo(ctx context.Context, caller trust.Identity, owner, name string) (access.RepoRef, error) {
	u := c.core + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return access.RepoRef{}, err
	}
	trust.Sign(req.Header, c.secret, caller, c.now())
	resp, err := c.http.Do(req)
	if err != nil {
		return access.RepoRef{}, errors.New(sanitize(err))
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusBadRequest:
		return access.RepoRef{}, access.ErrNotFound
	default:
		return access.RepoRef{}, fmt.Errorf("core ha risposto %d", resp.StatusCode)
	}
	var out struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Owner struct {
			Name string `json:"name"`
		} `json:"owner"`
		Archived             bool   `json:"archived"`
		DefaultBranch        string `json:"defaultBranch"`
		ProtectDefaultBranch bool   `json:"protectDefaultBranch"`
		RequirePullRequest   bool   `json:"requirePullRequest"`
		Mirror               *struct {
			Origin string `json:"origin"`
		} `json:"mirror"`
	}
	if err := json.Unmarshal(data, &out); err != nil || out.ID == "" {
		return access.RepoRef{}, errors.New("risposta di core non valida")
	}
	ref := access.RepoRef{ID: out.ID, Owner: out.Owner.Name, Name: out.Name, Archived: out.Archived, DefaultBranch: out.DefaultBranch, ProtectDefaultBranch: out.ProtectDefaultBranch, RequirePullRequest: out.RequirePullRequest}
	if out.Mirror != nil {
		ref.MirrorOrigin = out.Mirror.Origin
	}
	return ref, nil
}

// LookupKey implementa access.Keys: GET identity /internal/ssh-keys/{fp}
// (il fingerprint contiene '/' e '+': va escapato come segmento). Un utente
// senza isActive nella risposta è trattato come non attivo.
func (c *Client) LookupKey(ctx context.Context, fingerprint string) (access.KeyOwner, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.identity+"/internal/ssh-keys/"+url.PathEscape(fingerprint), nil)
	if err != nil {
		return access.KeyOwner{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.secret)
	resp, err := c.http.Do(req)
	if err != nil {
		return access.KeyOwner{}, fmt.Errorf("%w: %s", access.ErrUnavailable, sanitize(err))
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return access.KeyOwner{}, access.ErrUnknownKey
	default:
		return access.KeyOwner{}, fmt.Errorf("%w: ssh-keys ha risposto %d", access.ErrUnavailable, resp.StatusCode)
	}
	var out struct {
		User struct {
			ID       string `json:"id"`
			Username string `json:"username"`
			IsActive *bool  `json:"isActive"`
			Kind     string `json:"kind"`
		} `json:"user"`
	}
	if err := json.Unmarshal(data, &out); err != nil || out.User.ID == "" {
		return access.KeyOwner{}, fmt.Errorf("%w: risposta di ssh-keys non valida", access.ErrUnavailable)
	}
	return access.KeyOwner{UserID: out.User.ID, Username: out.User.Username, Kind: out.User.Kind, Active: out.User.IsActive != nil && *out.User.IsActive}, nil
}

// WithCIGateway abilita JobAccess: base è l'URL interno del ci-gateway di core.
func (c *Client) WithCIGateway(base string) *Client {
	c.ciGateway = strings.TrimRight(base, "/")
	return c
}

var _ access.Jobs = (*Client)(nil)

// JobAccess implementa access.Jobs: GET /ci/v1/registry-access del ci-gateway, con il
// token del job come Bearer. Il ci-gateway risponde solo per il repo del job.
func (c *Client) JobAccess(ctx context.Context, token, owner, repo string) (access.JobAccess, error) {
	if c.ciGateway == "" {
		return access.JobAccess{}, fmt.Errorf("%w: ci-gateway non configurato", access.ErrUnavailable)
	}
	if token == "" || len(token) > 128 {
		return access.JobAccess{}, access.ErrInvalidJobToken
	}
	q := url.Values{"owner": {owner}, "repo": {repo}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.ciGateway+"/ci/v1/registry-access?"+q.Encode(), nil)
	if err != nil {
		return access.JobAccess{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.http.Do(req)
	if err != nil {
		return access.JobAccess{}, fmt.Errorf("%w: %s", access.ErrUnavailable, sanitize(err))
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return access.JobAccess{}, access.ErrInvalidJobToken
	case http.StatusNotFound:
		return access.JobAccess{}, access.ErrNotFound
	default:
		return access.JobAccess{}, fmt.Errorf("%w: registry-access ha risposto %d", access.ErrUnavailable, resp.StatusCode)
	}
	var out struct {
		RepoID   string `json:"repoId"`
		Owner    string `json:"owner"`
		Name     string `json:"name"`
		Archived bool   `json:"archived"`
		Packages string `json:"packages"`
		Workflow string `json:"workflow"`
		Job      string `json:"job"`
		RunID    string `json:"runId"`
		Ref      string `json:"ref"`
		Event    string `json:"event"`
		ActorID  string `json:"actorId"`
	}
	if err := json.Unmarshal(data, &out); err != nil || out.RepoID == "" {
		return access.JobAccess{}, fmt.Errorf("%w: risposta di registry-access non valida", access.ErrUnavailable)
	}
	return access.JobAccess{RepoID: out.RepoID, Owner: out.Owner, Name: out.Name, Archived: out.Archived, Packages: out.Packages, Workflow: out.Workflow, Job: out.Job,
		RunID: out.RunID, Ref: out.Ref, Event: out.Event, ActorID: out.ActorID}, nil
}

// registrySettingsIdentity è l'identità con cui il servizio git chiama la rotta interna di
// core per le impostazioni del registry: non è un utente, core la accetta solo con la chiave
// del servizio git e solo per quella rotta.
var registrySettingsIdentity = trust.Identity{UserID: "00000000-0000-0000-0000-000000000000", Username: "gitstack-registry"}

var _ registry.SettingsSource = (*Client)(nil)

// PackageSettings implementa registry.SettingsSource: GET /internal/registry/settings/{repoId}
// di core. 404 = repo sconosciuto o eliminato.
func (c *Client) PackageSettings(ctx context.Context, repoID string) (registry.Settings, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.core+"/internal/registry/settings/"+url.PathEscape(repoID), nil)
	if err != nil {
		return registry.Settings{}, err
	}
	trust.Sign(req.Header, c.secret, registrySettingsIdentity, c.now())
	resp, err := c.http.Do(req)
	if err != nil {
		return registry.Settings{}, errors.New(sanitize(err))
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return registry.Settings{}, registry.ErrSettingsNotFound
	default:
		return registry.Settings{}, fmt.Errorf("core ha risposto %d", resp.StatusCode)
	}
	var out struct {
		RepoID                  string   `json:"repoId"`
		Owner                   string   `json:"owner"`
		Name                    string   `json:"name"`
		Archived                bool     `json:"archived"`
		ProtectedTagPatterns    []string `json:"protectedTagPatterns"`
		DeleteUntaggedAfterDays int      `json:"deleteUntaggedAfterDays"`
		KeepLastTags            *int     `json:"keepLastTags"`
		DeleteShaTagsAfterDays  *int     `json:"deleteShaTagsAfterDays"`
		IncludeInBackup         bool     `json:"includeInBackup"`
		OwnerRepoIDs            []string `json:"ownerRepoIds"`
	}
	if err := json.Unmarshal(data, &out); err != nil || out.RepoID == "" {
		return registry.Settings{}, errors.New("risposta di core non valida")
	}
	st := registry.Settings{RepoID: out.RepoID, Owner: out.Owner, Name: out.Name, Archived: out.Archived,
		ProtectedTagPatterns: out.ProtectedTagPatterns, DeleteUntaggedAfterDays: out.DeleteUntaggedAfterDays,
		IncludeInBackup: out.IncludeInBackup, OwnerRepoIDs: out.OwnerRepoIDs}
	if out.KeepLastTags != nil {
		st.KeepLastTags = *out.KeepLastTags
	}
	if out.DeleteShaTagsAfterDays != nil {
		st.DeleteShaTagsAfterDays = *out.DeleteShaTagsAfterDays
	}
	return st, nil
}

// RecordRemovedTag implementa registry.SettingsSource e salva in core un tag tolto da RG2.
func (c *Client) RecordRemovedTag(ctx context.Context, repoID string, tag registry.RemovedTag) error {
	b, err := json.Marshal(tag)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.core+"/internal/registry/removed-tags/"+url.PathEscape(repoID), bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	trust.Sign(req.Header, c.secret, registrySettingsIdentity, c.now())
	resp, err := c.http.Do(req)
	if err != nil {
		return errors.New(sanitize(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("core ha risposto %d", resp.StatusCode)
	}
	return nil
}

// RemovedTags implementa registry.SettingsSource e legge da core il registro della pulizia.
func (c *Client) RemovedTags(ctx context.Context, repoID string) ([]registry.RemovedTag, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.core+"/internal/registry/removed-tags/"+url.PathEscape(repoID), nil)
	if err != nil {
		return nil, err
	}
	trust.Sign(req.Header, c.secret, registrySettingsIdentity, c.now())
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errors.New(sanitize(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, registry.ErrSettingsNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("core ha risposto %d", resp.StatusCode)
	}
	var out struct {
		Items []registry.RemovedTag `json:"items"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return nil, errors.New("risposta di core non valida")
	}
	return out.Items, nil
}

var _ registry.PushNotifier = (*Client)(nil)

// ImagePushed implementa registry.PushNotifier: POST /internal/registry/image-pushed/{repoId} di
// core (H7, GIT-349). Core decide se qualche app segue il branch del run.
func (c *Client) ImagePushed(ctx context.Context, repoID string, push registry.ImagePush) error {
	b, err := json.Marshal(push)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.core+"/internal/registry/image-pushed/"+url.PathEscape(repoID), bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	trust.Sign(req.Header, c.secret, registrySettingsIdentity, c.now())
	resp, err := c.http.Do(req)
	if err != nil {
		return errors.New(sanitize(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("core ha risposto %d", resp.StatusCode)
	}
	return nil
}
