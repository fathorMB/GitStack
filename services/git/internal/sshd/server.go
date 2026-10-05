// Package sshd è il server SSH integrato del servizio git: solo
// autenticazione a chiave pubblica, nessuna shell, solo git-upload-pack e
// git-receive-pack su <owner>/<repo>.git. L'utente si trova dal fingerprint
// della chiave (identity), i permessi sono quelli di tutto GitStack.
package sshd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/fathorMB/GitStack/services/git/internal/access"
)

// LoginUser è l'unico nome di login accettato: ssh://git@host:2222/...
const LoginUser = "git"

// Authorizer è la parte di access.Authorizer usata dal server: lo stesso
// controllo dei permessi dello smart HTTP (M-03/H).
type Authorizer interface {
	Authorize(ctx context.Context, p access.Principal, owner, name string, write bool) (string, error)
}

// sshScopes: una chiave SSH vale come l'utente intero, senza la limitazione
// di scope dei token personali; restano ruolo e archiviazione.
var sshScopes = []string{access.ScopeRead, access.ScopeWrite}

// Config è la configurazione del server.
type Config struct {
	Addr      string
	HostKey   ssh.Signer
	Auth      Authorizer
	Keys      access.Keys
	Logger    *slog.Logger
	// GitBin è il binario git (vuoto = "git").
	GitBin string
	// HandshakeTimeout limita la fase prima dell'autenticazione (0 = 30s).
	HandshakeTimeout time.Duration
}

// Server è il server SSH.
type Server struct {
	cfg  Config
	ssh  *ssh.ServerConfig
	mu   sync.Mutex
	ln   net.Listener
	conn map[net.Conn]struct{}
	wg   sync.WaitGroup
	done bool
}

const (
	extUserID   = "gitstack-user-id"
	extUsername = "gitstack-username"
)

// New prepara il server.
func New(cfg Config) (*Server, error) {
	if cfg.HostKey == nil || cfg.Auth == nil || cfg.Keys == nil {
		return nil, errors.New("sshd: servono chiave host, autorizzatore e ricerca delle chiavi")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	if cfg.GitBin == "" {
		cfg.GitBin = "git"
	}
	if cfg.HandshakeTimeout == 0 {
		cfg.HandshakeTimeout = 30 * time.Second
	}
	s := &Server{cfg: cfg, conn: map[net.Conn]struct{}{}}
	sc := &ssh.ServerConfig{
		// Solo chiave pubblica: password e keyboard-interactive non sono
		// impostate, quindi il server non le offre.
		PublicKeyCallback: s.authKey,
		ServerVersion:     "SSH-2.0-GitStack",
		MaxAuthTries:      6,
	}
	sc.AddHostKey(cfg.HostKey)
	s.ssh = sc
	return s, nil
}

// authKey accetta la chiave solo se appartiene a un utente attivo.
func (s *Server) authKey(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	if c.User() != LoginUser {
		return nil, fmt.Errorf("utente %q non ammesso", c.User())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	fp := ssh.FingerprintSHA256(key)
	u, err := s.cfg.Keys.LookupKey(ctx, fp)
	if err != nil {
		if !errors.Is(err, access.ErrUnknownKey) {
			s.cfg.Logger.Warn("ssh: ricerca della chiave non riuscita", "err", err)
		}
		return nil, errors.New("chiave non autorizzata")
	}
	if !u.Active || u.UserID == "" {
		s.cfg.Logger.Info("ssh: utente disattivato", "user", u.Username)
		return nil, errors.New("chiave non autorizzata")
	}
	return &ssh.Permissions{Extensions: map[string]string{extUserID: u.UserID, extUsername: u.Username}}, nil
}

// Listen apre la porta; Addr restituisce poi l'indirizzo effettivo.
func (s *Server) Listen() error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()
	return nil
}

// Addr è l'indirizzo di ascolto (dopo Listen).
func (s *Server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return nil
	}
	return s.ln.Addr()
}

// Serve accetta connessioni fino a Close; dopo Close restituisce nil.
func (s *Server) Serve() error {
	s.mu.Lock()
	ln := s.ln
	s.mu.Unlock()
	if ln == nil {
		return errors.New("sshd: Listen non chiamata")
	}
	for {
		nc, err := ln.Accept()
		if err != nil {
			s.mu.Lock()
			done := s.done
			s.mu.Unlock()
			if done {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return err
		}
		s.mu.Lock()
		if s.done {
			s.mu.Unlock()
			_ = nc.Close()
			return nil
		}
		s.conn[nc] = struct{}{}
		s.wg.Add(1)
		s.mu.Unlock()
		go func() {
			defer s.wg.Done()
			s.handle(nc)
			s.mu.Lock()
			delete(s.conn, nc)
			s.mu.Unlock()
		}()
	}
}

// Close ferma il server e chiude le connessioni aperte.
func (s *Server) Close() error {
	s.mu.Lock()
	s.done = true
	ln := s.ln
	for c := range s.conn {
		_ = c.Close()
	}
	s.mu.Unlock()
	var err error
	if ln != nil {
		err = ln.Close()
	}
	s.wg.Wait()
	return err
}

func (s *Server) handle(nc net.Conn) {
	defer func() { _ = nc.Close() }()
	_ = nc.SetDeadline(time.Now().Add(s.cfg.HandshakeTimeout))
	sc, chans, reqs, err := ssh.NewServerConn(nc, s.ssh)
	if err != nil {
		return
	}
	_ = nc.SetDeadline(time.Time{})
	defer func() { _ = sc.Close() }()
	go ssh.DiscardRequests(reqs)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = sc.Wait(); cancel() }()

	user := access.Principal{
		UserID:   sc.Permissions.Extensions[extUserID],
		Username: sc.Permissions.Extensions[extUsername],
		Scopes:   sshScopes,
	}
	var wg sync.WaitGroup
	for nch := range chans {
		if nch.ChannelType() != "session" {
			_ = nch.Reject(ssh.UnknownChannelType, "solo canali session")
			continue
		}
		ch, creqs, err := nch.Accept()
		if err != nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.session(ctx, user, ch, creqs)
		}()
	}
	wg.Wait()
}

var gitProtocolRe = regexp.MustCompile(`^version=[0-2]$`)

// session gestisce un canale: accetta solo "exec" (ed "env" per
// GIT_PROTOCOL); shell, pty, subsystem, forwarding e il resto sono rifiutati.
func (s *Server) session(ctx context.Context, u access.Principal, ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer func() { _ = ch.Close() }()
	protocol := ""
	for req := range reqs {
		switch req.Type {
		case "env":
			var p struct{ Name, Value string }
			if ssh.Unmarshal(req.Payload, &p) == nil && p.Name == "GIT_PROTOCOL" && gitProtocolRe.MatchString(p.Value) {
				protocol = p.Value
				_ = req.Reply(true, nil)
				continue
			}
			_ = req.Reply(false, nil)
		case "exec":
			var p struct{ Command string }
			if ssh.Unmarshal(req.Payload, &p) != nil {
				_ = req.Reply(false, nil)
				return
			}
			svc, owner, name, err := parseCommand(p.Command)
			if err != nil {
				_ = req.Reply(true, nil) // il messaggio d'errore va sul canale
				s.fail(ch, 128, "comando non consentito: sono ammessi solo git-upload-pack e git-receive-pack")
				return
			}
			_ = req.Reply(true, nil)
			s.run(ctx, u, ch, svc, owner, name, protocol)
			return
		default:
			_ = req.Reply(false, nil)
		}
	}
}

func (s *Server) fail(ch ssh.Channel, code uint32, msg string) {
	_, _ = io.WriteString(ch.Stderr(), "gitstack: "+msg+"\n")
	_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{code}))
}

// run autorizza e serve un upload-pack o receive-pack.
func (s *Server) run(ctx context.Context, u access.Principal, ch ssh.Channel, svc, owner, name, protocol string) {
	actx, cancel := context.WithTimeout(ctx, 15*time.Second)
	path, err := s.cfg.Auth.Authorize(actx, u, owner, name, svc == "receive-pack")
	cancel()
	switch {
	case err == nil:
	case errors.Is(err, access.ErrNotFound):
		// Stesso messaggio per «non esiste» e «non puoi leggerlo».
		s.cfg.Logger.Info("ssh: repo non trovato o non leggibile", "user", u.Username, "repo", owner+"/"+name)
		s.fail(ch, 1, "repo non trovato o accesso negato")
		return
	case errors.Is(err, access.ErrForbidden):
		s.cfg.Logger.Info("ssh: accesso negato", "user", u.Username, "repo", owner+"/"+name, "op", svc)
		s.fail(ch, 1, "accesso negato: serve il ruolo write")
		return
	case errors.Is(err, access.ErrArchived):
		s.cfg.Logger.Info("ssh: push su repo archiviato", "user", u.Username, "repo", owner+"/"+name)
		s.fail(ch, 1, "il repo è archiviato: push non consentito")
		return
	default:
		s.cfg.Logger.Warn("ssh: autorizzazione non riuscita", "err", err)
		s.fail(ch, 1, "servizio temporaneamente non disponibile")
		return
	}

	cmd := exec.CommandContext(ctx, s.cfg.GitBin, svc, filepath.Clean(path))
	cmd.Env = gitEnv(protocol)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		s.fail(ch, 1, "servizio temporaneamente non disponibile")
		return
	}
	cmd.Stdout = ch
	cmd.Stderr = ch.Stderr()
	if err := cmd.Start(); err != nil {
		s.cfg.Logger.Warn("ssh: git non avviabile", "err", err)
		s.fail(ch, 1, "servizio temporaneamente non disponibile")
		return
	}
	// Copia dello stdin a mano: Wait non deve aspettare l'EOF del client.
	go func() { _, _ = io.Copy(stdin, ch); _ = stdin.Close() }()
	code := uint32(0)
	if err := cmd.Wait(); err != nil {
		code = 1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = uint32(ee.ExitCode()) //nolint:gosec // exit code non negativo qui
		}
		s.cfg.Logger.Info("ssh: git terminato con errore", "user", u.Username, "op", svc, "err", err)
	}
	_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{code}))
	_ = ch.CloseWrite()
}

// gitEnv è l'ambiente di git: senza GIT_* ereditate, senza configurazione
// di sistema o dell'utente, senza prompt.
func gitEnv(protocol string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(strings.ToUpper(kv), "GIT_") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
	if protocol != "" {
		env = append(env, "GIT_PROTOCOL="+protocol)
	}
	return env
}

var repoPathRe = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]{0,38})/([A-Za-z0-9._-]{1,100}?)(?:\.git)?$`)

// parseCommand accetta solo `git-upload-pack <path>` e `git-receive-pack
// <path>` (anche `git upload-pack`), con il percorso fra apici singoli come
// lo scrive git o nudo, e <path> = [/]owner/repo[.git]. Tutto il resto è
// rifiutato: niente shell, niente argomenti extra.
func parseCommand(cmd string) (svc, owner, name string, err error) {
	cmd = strings.TrimSpace(cmd)
	var rest string
	switch {
	case strings.HasPrefix(cmd, "git-upload-pack "):
		svc, rest = "upload-pack", cmd[len("git-upload-pack "):]
	case strings.HasPrefix(cmd, "git-receive-pack "):
		svc, rest = "receive-pack", cmd[len("git-receive-pack "):]
	case strings.HasPrefix(cmd, "git upload-pack "):
		svc, rest = "upload-pack", cmd[len("git upload-pack "):]
	case strings.HasPrefix(cmd, "git receive-pack "):
		svc, rest = "receive-pack", cmd[len("git receive-pack "):]
	default:
		return "", "", "", errors.New("comando non ammesso")
	}
	rest = strings.TrimSpace(rest)
	if len(rest) >= 2 && rest[0] == '\'' && rest[len(rest)-1] == '\'' {
		rest = rest[1 : len(rest)-1]
	}
	// Dopo aver tolto gli apici non devono restare apici, spazi o
	// metacaratteri: il percorso è validato per intero dalla regex.
	rest = strings.TrimPrefix(rest, "/")
	m := repoPathRe.FindStringSubmatch(rest)
	if m == nil || strings.HasPrefix(m[2], ".") || strings.HasSuffix(m[2], ".") {
		return "", "", "", errors.New("percorso non valido")
	}
	return svc, m[1], m[2], nil
}
