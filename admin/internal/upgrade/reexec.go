package upgrade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// Re-exec (GIT-490).
//
// Il rollback e i passi dopo un upgrade riuscito (Secret di servizio, certificati del cliente)
// cambiano da versione a versione, ma li eseguiva il binario già installato, che viene
// sostituito solo dopo waitHealthy: chi aggiornava da una versione senza la correzione di
// GIT-479/GIT-482 faceva il rollback con la logica vecchia. Ora, se il binario di destinazione
// (già scaricato e verificato in `staged`) lo supporta, è lui a eseguire quei passi:
// `<staged> upgrade --reexec-request F --reexec-reply F`. Se non lo supporta (destinazione
// precedente a GIT-490) o non parte, si ripiega sulla logica di questo binario.
//
// Il binario vecchio, quello che oggi è installato, non può essere cambiato: il re-exec vale
// per gli upgrade lanciati da una versione che contiene GIT-490. Da una versione precedente
// (v1.0 compresa) resta il rollback in-process: la procedura manuale è in docs/operations.md.

// ReexecProtocol è la versione del protocollo: `<bin> upgrade --reexec-protocol` la stampa.
const ReexecProtocol = "1"

// Modi di ReexecRequest.
const (
	ReexecRollback = "rollback" // helm rollback, pulizia dei ServiceAccount, restore, salute
	ReexecPost     = "post"     // passi dopo un upgrade riuscito
)

// ErrNoReexec: il binario di destinazione non supporta il re-exec, nessun passo è stato eseguito.
var ErrNoReexec = errors.New("il binario di destinazione non supporta il re-exec")

// ReexecRequest è lo stato che il processo vecchio passa al nuovo (JSON su file).
type ReexecRequest struct {
	Mode       string `json:"mode"`
	From       string `json:"from"`
	To         string `json:"to"`
	Reason     string `json:"reason,omitempty"`
	PrevRev    int    `json:"prev_rev,omitempty"`
	Before     string `json:"before,omitempty"`
	BeforeErr  string `json:"before_err,omitempty"`
	BackupPath string `json:"backup_path,omitempty"`
	HelmRan    bool   `json:"helm_ran,omitempty"`
	// UserServiceSecret serve a ReexecPost (removeLegacyServiceSecret).
	UserServiceSecret string `json:"user_service_secret,omitempty"`
}

// ReexecReply è l'esito del processo nuovo.
type ReexecReply struct {
	Outcome         Outcome  `json:"outcome"`
	RolledBackChart bool     `json:"rolled_back_chart,omitempty"`
	Restored        bool     `json:"restored,omitempty"`
	RollbackErr     string   `json:"rollback_err,omitempty"`
	Summary         []string `json:"summary,omitempty"`
}

// ReadReexecRequest legge la richiesta scritta dal processo vecchio.
func ReadReexecRequest(path string) (*ReexecRequest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r ReexecRequest
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("richiesta di re-exec: %w", err)
	}
	return &r, nil
}

// WriteJSON scrive v come JSON, con permessi 0600.
func WriteJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// ReadReexecReply legge la risposta del processo nuovo.
func ReadReexecReply(path string) (*ReexecReply, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r ReexecReply
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("risposta di re-exec: %w", err)
	}
	return &r, nil
}

// RunReexec è il lato del processo nuovo: esegue il passo richiesto con la propria logica.
func RunReexec(ctx context.Context, o *Options, req *ReexecRequest) (*ReexecReply, error) {
	res := &Result{From: req.From, To: req.To, Reason: req.Reason}
	switch req.Mode {
	case ReexecRollback:
		st := &state{o: o, res: res, prevRev: req.PrevRev, before: req.Before, backupPath: req.BackupPath, helmRan: req.HelmRan}
		if req.BeforeErr != "" {
			st.beforeErr = errors.New(req.BeforeErr)
		}
		o.rollbackCluster(st)
	case ReexecPost:
		o.postUpgradeSteps(ctx, res, req.UserServiceSecret)
		res.Outcome = Upgraded
	default:
		return nil, fmt.Errorf("modo di re-exec sconosciuto: %q", req.Mode)
	}
	return &ReexecReply{
		Outcome: res.Outcome, RolledBackChart: res.RolledBackChart, Restored: res.Restored,
		RollbackErr: res.RollbackErr, Summary: res.Summary,
	}, nil
}

// reexec chiede a `staged` di eseguire req. ok=false: non ha eseguito niente (re-exec non
// disponibile), si prosegue con la logica interna.
func (o *Options) reexec(ctx context.Context, staged string, req *ReexecRequest) (*ReexecReply, bool) {
	if o.Reexec == nil || staged == "" {
		return nil, false
	}
	o.logf("==> Eseguo il passo «%s» con il binario di destinazione %s", req.Mode, req.To)
	reply, err := o.Reexec(ctx, staged, req)
	if err != nil {
		if errors.Is(err, ErrNoReexec) {
			o.logf("    il binario di destinazione non supporta il re-exec: uso la logica di questa versione")
		} else {
			o.logf("!! re-exec fallito (%v): uso la logica di questa versione", err)
		}
		return nil, false
	}
	return reply, true
}

// postUpgradeSteps sono i passi dopo un upgrade riuscito. Mai fatali.
func (o *Options) postUpgradeSteps(ctx context.Context, res *Result, userServiceSecret string) {
	o.removeLegacyServiceSecret(ctx, res, userServiceSecret)
	o.migrateCustomCerts(ctx, res)
}

// postUpgrade li esegue con il binario di destinazione, se può.
func (o *Options) postUpgrade(ctx context.Context, res *Result, staged, userServiceSecret string) {
	req := &ReexecRequest{Mode: ReexecPost, From: res.From, To: res.To, UserServiceSecret: userServiceSecret}
	if reply, ok := o.reexec(ctx, staged, req); ok {
		res.Summary = append(res.Summary, reply.Summary...)
		return
	}
	o.postUpgradeSteps(ctx, res, userServiceSecret)
}
