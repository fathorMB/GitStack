//go:build integration

package stackitest

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/pkg/events"
	"github.com/fathorMB/GitStack/services/core/internal/issuelinks"
)

// M-07/M (GIT-174): gli script della prova reale (scripts/agent-trial) contro
// lo stack completo, col binario gs vero. setup.sh prepara agente, repo,
// grant, issue e token; poi si recita la parte dell'agente (notifica, clone,
// commit `fixes #n` su un ramo agent/<n>-<slug>, push del ramo, risposta, lettura);
// poi una persona unisce sul ramo predefinito (regola v1 senza PR, GIT-191) e
// check.sh deve dare 0.
// Prima che l'agente lavori check.sh deve dare 1; dopo il lavoro ma prima
// dell'unione dà 3, con i controlli del passo di unione in SKIP (non FAIL).

func TestAgentTrial(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash non è nel PATH")
	}
	g := newGsEnv(t)
	scripts, err := filepath.Abs("../../../../scripts/agent-trial")
	if err != nil {
		t.Fatal(err)
	}

	// Token dell'amministratore con gli scope che la guida chiede (write:user, write:resource).
	login := g.gw("POST", "/auth/login", map[string]any{"username": "admin", "password": newPassword}, nil, nil)
	want(t, login, 200, "")
	tok := g.gw("POST", "/user/tokens", map[string]any{
		"name": "agent-trial-setup", "scopes": []string{"write:user", "write:resource"},
		"expiresAt": time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339),
	}, nil, g.session(login))
	want(t, tok, 201, "")
	adminToken, _ := tok.json()["token"].(string)
	if adminToken == "" {
		t.Fatal("token admin vuoto")
	}

	work := t.TempDir()
	tokenFile := filepath.Join(work, "agent-trial.token")
	// Nello stack di prova gli indirizzi di clone puntano al servizio git, non
	// al gateway: le credenziali di git si danno con insteadOf (in produzione
	// ci pensa `gs auth setup-git`, che vale per l'host dell'istanza).
	gitEnv := func(token string) []string {
		return []string{
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=url." + strings.Replace(g.gitHTTP, "http://", "http://x:"+token+"@", 1) + ".insteadOf",
			"GIT_CONFIG_VALUE_0=" + g.gitHTTP,
		}
	}
	run := func(script string, extra []string, args ...string) (string, int) {
		t.Helper()
		cmd := exec.Command("bash", append([]string{filepath.Join(scripts, script)}, args...)...)
		cmd.Dir = work
		cmd.Env = append(cleanEnv(), "GS="+g.bin, "GS_HOST="+g.proxy, "GS_ADMIN_TOKEN="+adminToken,
			"TRIAL_TOKEN_FILE="+tokenFile, "HOME="+g.home, "USERPROFILE="+g.home)
		cmd.Env = append(cmd.Env, extra...)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		code := 0
		if err := cmd.Run(); err != nil {
			ee, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("%s: %v", script, err)
			}
			code = ee.ExitCode()
		}
		return out.String(), code
	}

	out, code := run("setup.sh", gitEnv(adminToken))
	if code != 0 {
		t.Fatalf("setup.sh: exit %d\n%s", code, out)
	}
	for _, s := range []string{"ok agente (trial-agent)", "ok repo (admin/agent-trial)", "ok issue (#1 assegnata a trial-agent)", "ok token (scope read:resource write:resource"} {
		if !strings.Contains(out, s) {
			t.Errorf("setup.sh: manca %q\n%s", s, out)
		}
	}
	raw, err := os.ReadFile(tokenFile)
	if err != nil {
		t.Fatalf("file del token: %v", err)
	}
	agentToken := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(agentToken, "gst_") || strings.Contains(out, agentToken) {
		t.Fatalf("token dell'agente non valido o stampato: %q", agentToken[:4])
	}

	// Idempotenza: nessuna issue, utente o token in più.
	out2, code := run("setup.sh", gitEnv(adminToken))
	if code != 0 {
		t.Fatalf("secondo setup.sh: exit %d\n%s", code, out2)
	}
	if !strings.Contains(out2, "ok issue (#1 ") || !strings.Contains(out2, "esiste gia' un token") {
		t.Errorf("il secondo setup.sh non è idempotente:\n%s", out2)
	}
	if again, _ := os.ReadFile(tokenFile); strings.TrimSpace(string(again)) != agentToken {
		t.Error("il secondo setup.sh ha cambiato il token")
	}
	g.process()

	// Prima del lavoro dell'agente la checklist non passa.
	if out, code := run("check.sh", nil); code != 1 || !strings.Contains(out, "FAIL ramo dell'agente") ||
		!strings.Contains(out, "nessun commento di trial-agent") || strings.Contains(out, "cannot iterate") {
		t.Fatalf("check.sh prima del lavoro: exit %d\n%s", code, out)
	}

	// L'agente: legge la notifica, clona, lavora su un ramo, committa con fixes #1
	// (-F, come dicono le skills) con la propria identità git, spinge il ramo.
	as := func(args ...string) gsRun {
		t.Helper()
		r := g.gsToken(agentToken, "", "", args...)
		if r.code != 0 {
			t.Fatalf("gs %v come trial-agent: exit %d: %s", args, r.code, r.errOut)
		}
		return r
	}
	if r := as("notification", "list", "--json", "reason,issue", "--jq", `.[] | select(.reason == "assigned") | .issue.number`); strings.TrimSpace(r.out) != "1" {
		t.Fatalf("notifica di assegnazione dell'agente: %q", r.out)
	}
	dir := filepath.Join(t.TempDir(), "w")
	const agentEmail = "trial-agent@agents.gitstack.invalid" // quella che setup.sh dà all'utente agente
	// gitAs lancia git con le credenziali di token e con l'identità data.
	gitAs := func(token, name, email, d string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "credential.helper=", "-c", "core.autocrlf=false"}, args...)...)
		cmd.Dir = d
		cmd.Env = append(cleanEnv(), "HOME="+g.home, "USERPROFILE="+g.home, "GIT_TERMINAL_PROMPT=0",
			"GIT_AUTHOR_NAME="+name, "GIT_AUTHOR_EMAIL="+email,
			"GIT_COMMITTER_NAME="+name, "GIT_COMMITTER_EMAIL="+email)
		cmd.Env = append(cmd.Env, gitEnv(token)...)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
	gitAgent := func(d string, args ...string) {
		t.Helper()
		gitAs(agentToken, "trial-agent", agentEmail, d, args...)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	gitAgent(filepath.Dir(dir), "clone", "-q", g.gitHTTP+"/admin/agent-trial.git", dir)
	// Identità del repo (config locale), come nel passo 4 della guida.
	gitAgent(dir, "config", "user.name", "trial-agent")
	gitAgent(dir, "config", "user.email", agentEmail)
	branch := "agent/1-conta-parole"
	gitAgent(dir, "switch", "-q", "-c", branch)
	fixed := "import sys\n\n\ndef count_words(text):\n    return len(text.split())\n\n\nif __name__ == \"__main__\":\n    print(count_words(sys.stdin.read()))\n"
	if err := os.WriteFile(filepath.Join(dir, "wordcount.py"), []byte(fixed), 0o644); err != nil {
		t.Fatal(err)
	}
	msg := filepath.Join(work, "msg.txt")
	if err := os.WriteFile(msg, []byte("Conta le parole con qualsiasi spazio bianco\n\nfixes #1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAgent(dir, "commit", "-q", "-a", "-F", msg)
	gitAgent(dir, "push", "-q", "-u", "origin", branch)

	id, ok := g.notifier.Identity.(issuelinks.Identity)
	if !ok {
		t.Fatal("l'identity dello stack non ha HasRole")
	}
	linker := &issuelinks.Linker{Pool: g.notifier.Pool, Identity: id, Notifier: g.notifier}
	// pushEvents rende tutti i push del repo dall'inizio (seed di setup.sh incluso):
	// si gestiscono solo quelli non ancora visti.
	handled := 0
	process := func(total int) {
		t.Helper()
		evs := g.pushEvents("admin/agent-trial", total)
		for _, p := range evs[handled:] {
			if err := linker.HandlePush(context.Background(), events.Envelope{}, p); err != nil {
				t.Fatalf("HandlePush: %v", err)
			}
		}
		handled = len(evs)
	}
	process(2) // il seed di setup.sh e il ramo dell'agente
	// Il commit è dell'agente e non della persona: GitStack lo collega all'utente per email.
	if r := g.gsToken(adminToken, "", "", "api", "/repos/admin/agent-trial/commits?ref=agent%2F1-conta-parole",
		"--jq", ".items[0].author.user.username"); r.code != 0 || strings.TrimSpace(r.out) != "trial-agent" {
		t.Errorf("autore del commit dell'agente: exit %d, %q (%s)", r.code, r.out, r.errOut)
	}
	cmt := filepath.Join(work, "comment.txt")
	if err := os.WriteFile(cmt, []byte("Fatto nel ramo "+branch+"; i test passano. Riassunto: count_words con split().\n\n"+
		"Per unire, dal ramo predefinito:\ngit fetch && git merge --ff-only origin/"+branch+" && git push\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	as("issue", "comment", "1", "-R", "admin/agent-trial", "-F", cmt)
	as("notification", "read", "--all", "-R", "admin/agent-trial", "--reason", "assigned")

	transcript := filepath.Join(work, "sessione.txt")
	if err := os.WriteFile(transcript, []byte("gs issue view 1 --comments\ngs issue comment 1 --body ok\ngs notification read --all\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Prima dell'unione: la parte dell'agente è verde, il passo di unione è SKIP
	// con il motivo (uscita 3), non FAIL.
	out, code = run("check.sh", []string{"TRIAL_TRANSCRIPT=" + transcript})
	if code != 3 || strings.Contains(out, "FAIL ") {
		t.Fatalf("check.sh prima dell'unione: exit %d, atteso 3 senza FAIL\n%s", code, out)
	}
	for _, s := range []string{
		"PASS ramo remoto " + branch + " con 'fixes #1'",
		"PASS il commento dell'agente nomina il ramo " + branch,
		"SKIP commit fixes sul ramo predefinito: la persona non ha ancora unito",
		"SKIP issue chiusa come completed: la persona non ha ancora unito",
		"PASS la notifica di assegnazione della issue #1 e' stata letta",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("check.sh prima dell'unione: manca %q\n%s", s, out)
		}
	}

	// La persona (admin, con write) unisce in fast-forward sul ramo predefinito:
	// il `fixes #1` arriva su main e chiude la issue, e la chiusura genera una
	// notifica state_change nella casella dell'agente quando ha già finito.
	gitAs(adminToken, "admin", "admin@example.com", dir, "push", "-q", "origin", branch+":main")
	process(3)
	out, code = run("check.sh", []string{"TRIAL_TRANSCRIPT=" + transcript})
	if code != 0 {
		t.Fatalf("check.sh dopo l'unione: exit %d\n%s", code, out)
	} else if !strings.Contains(out, "ESITO: prova superata") || !strings.Contains(out, "PASS issue #1 chiusa come completed") ||
		!strings.Contains(out, "PASS commit con 'fixes #1' sul ramo predefinito") {
		t.Fatalf("check.sh dopo l'unione: esito inatteso\n%s", out)
	}
	// La chiusura ha prodotto una notifica non letta di altro tipo: non conta.
	if r := as("notification", "list", "-R", "admin/agent-trial", "--json", "reason", "--jq", ".[].reason"); strings.Contains(r.out, "assigned") {
		t.Errorf("notifica di assegnazione ancora non letta: %q", r.out)
	}

	// Un --yes nella trascrizione fa fallire il controllo.
	if err := os.WriteFile(transcript, []byte("gs repo delete admin/agent-trial --yes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := run("check.sh", []string{"TRIAL_TRANSCRIPT=" + transcript}); code != 1 || !strings.Contains(out, "FAIL nessun --yes improprio") {
		t.Fatalf("check.sh con --yes: exit %d\n%s", code, out)
	}
}
