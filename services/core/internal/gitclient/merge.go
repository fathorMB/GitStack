package gitclient

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
)

// Merge e eliminazione dei branch (GIT-263): le scritture del servizio git
// sulle Pull Request (GIT-260). Le letture (compare, mergeability) passano da
// Reader.

// Identity è nome ed email di autore o committer del commit di merge.
type Identity struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// Pusher è chi ha chiesto l'operazione (per l'evento git.push).
type Pusher struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Type     string `json:"type"`
}

// MergeRequest è il corpo di POST …/merge.
type MergeRequest struct {
	Base            string    `json:"base"`
	Head            string    `json:"head"`
	Method          string    `json:"method"`
	Message         string    `json:"message"`
	ExpectedBaseSha string    `json:"expectedBaseSha"`
	ExpectedHeadSha string    `json:"expectedHeadSha,omitempty"`
	Author          Identity  `json:"author"`
	Committer       *Identity `json:"committer,omitempty"`
	Owner           string    `json:"owner"`
	Name            string    `json:"name"`
	DefaultBranch   string    `json:"defaultBranch"`
	Pusher          Pusher    `json:"pusher"`
}

// MergeResult è l'esito del merge.
type MergeResult struct {
	Sha       string   `json:"sha"`
	Method    string   `json:"method"`
	Parents   []string `json:"parents"`
	BaseSha   string   `json:"baseSha"`
	HeadSha   string   `json:"headSha"`
	MergeBase string   `json:"mergeBase"`
}

// DeleteBranchRequest è il corpo di DELETE …/branches.
type DeleteBranchRequest struct {
	Branch        string `json:"branch"`
	ExpectedSha   string `json:"expectedSha,omitempty"`
	Owner         string `json:"owner"`
	Name          string `json:"name"`
	DefaultBranch string `json:"defaultBranch"`
	Pusher        Pusher `json:"pusher"`
}

// MergeError è un rifiuto del servizio git su una scrittura: porta il codice
// (conflicts, stale, nothing_to_merge, unrelated_histories, default_branch…) e,
// se c'è, l'elenco dei file in conflitto o lo sha corrente del ref.
type MergeError struct {
	Status     int
	Code       string
	Message    string
	Files      []string
	Ref        string
	CurrentSha string
}

func (e *MergeError) Error() string { return "git ha rifiutato l'operazione: " + e.Code }

// Merger sono le scritture di merge dell'API interna di git. Le implementa
// Client; core la cerca con un'asserzione di tipo, come Reader.
type Merger interface {
	Merge(ctx context.Context, caller trust.Identity, repoID uuid.UUID, in MergeRequest) (MergeResult, error)
	DeleteBranch(ctx context.Context, caller trust.Identity, repoID uuid.UUID, in DeleteBranchRequest) error
}

func (c *Client) writeJSON(ctx context.Context, caller trust.Identity, method, path string, in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	trust.Sign(req.Header, c.secret, caller, c.now())
	// Un merge può durare più del timeout delle letture: vale il contesto.
	resp, err := c.stream.Do(req)
	if err != nil {
		return transportError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusOK {
		if out != nil {
			if err := json.Unmarshal(data, out); err != nil {
				return transportError(err)
			}
		}
		return nil
	}
	switch resp.StatusCode {
	case http.StatusNotFound, http.StatusBadRequest, http.StatusConflict:
		var body struct {
			Error struct {
				Code       string   `json:"code"`
				Message    string   `json:"message"`
				Files      []string `json:"files"`
				Ref        string   `json:"ref"`
				CurrentSha string   `json:"currentSha"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &body)
		e := body.Error
		if e.Code == "" {
			e.Code = map[int]string{404: "not_found", 400: "invalid_request", 409: "conflict"}[resp.StatusCode]
		}
		return &MergeError{Status: resp.StatusCode, Code: e.Code, Message: e.Message, Files: e.Files, Ref: e.Ref, CurrentSha: e.CurrentSha}
	}
	return transportError(io.ErrUnexpectedEOF)
}

// Merge implementa Merger.
func (c *Client) Merge(ctx context.Context, caller trust.Identity, repoID uuid.UUID, in MergeRequest) (MergeResult, error) {
	var out MergeResult
	err := c.writeJSON(ctx, caller, http.MethodPost, "/internal/git/repos/"+repoID.String()+"/merge", in, &out)
	return out, err
}

// DeleteBranch implementa Merger.
func (c *Client) DeleteBranch(ctx context.Context, caller trust.Identity, repoID uuid.UUID, in DeleteBranchRequest) error {
	return c.writeJSON(ctx, caller, http.MethodDelete, "/internal/git/repos/"+repoID.String()+"/branches", in, nil)
}

var _ Merger = (*Client)(nil)
