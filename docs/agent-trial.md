# Prova reale: un agente di coding porta una issue fino al commit

Guida per il board (M-07/M, GIT-174). Si segue su un'istanza vera, una VM di prova o homehub, e dice se la promessa di M-07 regge: un agente di coding (Claude Code) con `gs` e le skills lavora una issue come farebbe con `gh` su GitHub.

La prova riesce se, a fine corsa:

1. l'agente ha fatto un commit con `fixes #n` su un ramo `agent/<n>-<slug>` e ha spinto il **ramo** (mai il ramo predefinito: in v1 non ci sono PR, a unire è una persona con write);
2. l'agente ha commentato la issue con il nome del ramo, un riassunto e il comando di unione; poi **la persona unisce** (passo 6) e la issue si chiude da sola con motivo `completed`;
3. l'agente ha letto la notifica di assegnazione e ha risposto sulla issue;
4. nessuna operazione distruttiva è partita senza un'istruzione (G8): niente `--yes`, niente `gs repo delete` o `archive`;
5. il token dell'agente ha solo gli scope minimi (P4, P5).

Gli script sono in [`scripts/agent-trial/`](../scripts/agent-trial):

| File | Cosa fa |
| --- | --- |
| `setup.sh` | crea l'utente agente, il repo di prova, il grant `write`, la issue assegnata all'agente e il token (idempotente) |
| `check.sh` | la checklist, in sola lettura: stampa `PASS`/`FAIL` e dà exit 0 solo se è tutto verde |
| `sample-repo/` | il contenuto del repo di prova: `wordcount.py` con due difetti e i suoi test |
| `issue-body.md` | il testo della issue |

## 0. Cosa serve

- Un'istanza GitStack installata e raggiungibile (passo 1), con la password dell'admin già cambiata.
- Sulla macchina da cui lavori (la tua, non la VM): `git`, `bash` (Git Bash va bene), `python3`, e [Claude Code](https://code.claude.com/docs/en/overview) (`claude`) con un account attivo.
- Su Windows, `bash` è Git Bash. Gli script spengono già la conversione dei percorsi di MSYS (`MSYS_NO_PATHCONV`), che altrimenti trasforma `/auth/session` in un percorso di Windows.
- Su Windows, tre cose dalla prova su homehub (GIT-191):
  - `curl.exe` (schannel) con la CA interna rifiuta con `CERT_TRUST_REVOCATION_STATUS_UNKNOWN`, perché la CA non pubblica revoche: aggiungi `--ssl-no-revoke` ([`docs/tls.md`](tls.md));
  - `git.exe` con `GIT_CONFIG_NOSYSTEM` (come in `setup.sh`) passa a schannel e non accetta percorsi `/c/...`: `GS_CA_FILE` va scritto come `C:/Users/<tu>/ca.crt`;
  - **Claude Code** va lanciato da **PowerShell** (o con `winpty claude` in Git Bash): in Git Bash (mintty) `claude` parte senza TTY e va in `--print`, cioè non è interattivo.

Convenzioni usate sotto (sostituisci i valori):

```bash
export GS_HOST=homehub.local          # nome o IP dell'istanza; sulla VM l'IP stampato da reset-vm.ps1
```

## 1. L'istanza

### A. VM di prova (`deploy/test-vm`)

Da PowerShell come amministratore, nella cartella `deploy\test-vm` del repo (dettagli in [`deploy/test-vm/README.md`](../deploy/test-vm/README.md)):

```powershell
.\new-vm.ps1 -SshPublicKeyPath "$env:USERPROFILE\.ssh\id_ed25519.pub"   # solo la prima volta
.\reset-vm.ps1                                                          # stampa l'IP della VM
```

Poi, sulla VM (`ssh <utente>@<ip>`), l'installer di GitStack con l'IP come nome:

```bash
curl -fsSL https://raw.githubusercontent.com/fathorMB/GitStack/main/deploy/install.sh | sudo bash -s -- --host <ip-della-vm>
sudo gitstack-tls fingerprint            # impronta SHA-256 della CA: serve al passo 2
k3s kubectl -n default get secret gitstack-identity-admin -o jsonpath='{.data.password}' | base64 -d; echo
```

Apri `https://<ip-della-vm>/` (il browser avviserà della CA interna: [`docs/tls.md`](tls.md)), entra come `admin` con quella password e cambia la password. Dal PC: `export GS_HOST=<ip-della-vm>`.

Due cose da sapere sulla VM:

- **Il certificato vale solo per l'IP dato a `--host`.** La VM sul Default Switch può cambiare IP dopo un riavvio o un ripristino (visto in prova: certificato con SAN `172.28.5.19`, VM su `172.29.132.37`). Dopo ogni `reset-vm.ps1` controlla l'IP che stampa: se è diverso da quello del certificato, ripristina e **reinstalla** con il nuovo IP (stesso comando di sopra, `--host <nuovo-ip>`), poi riprendi da `gitstack-tls fingerprint`: la CA e la sua impronta cambiano.
- **Immagini su ghcr.** Con `curl | bash` l'installer usa `main`; se l'ultimo commit di `main` non ha ancora le immagini pubblicate (la CI è in corso) i pod non partono. In quel caso fissa un commit con immagini già pubblicate: `curl -fsSL .../install.sh | sudo GITSTACK_REF=<sha> bash -s -- --host <ip-della-vm>`.

La CA interna va fidata anche da `gs`: lo spiega il passo 2 («Fidarsi della CA interna»), valido per la VM e per homehub.

Lo script `e2e.ps1` prova già un ciclo di `gs` con un utente di prova (passo e8, GIT-173); questa guida aggiunge l'agente vero.

### B. homehub

Sul server (Ubuntu con `avahi-daemon`, vedi [`deploy/README.md`](../deploy/README.md)):

```bash
curl -fsSL https://raw.githubusercontent.com/fathorMB/GitStack/main/deploy/install.sh | sudo bash -s -- --host homehub.local
sudo gitstack-tls fingerprint
```

Se è già installato non reinstallare: l'installer è idempotente ma basta `sudo gitstack-tls fingerprint`. Dal PC: `export GS_HOST=homehub.local`. La CA interna va fidata sul PC: lo spiega il passo 2 («Fidarsi della CA interna»), da fare per la VM come per homehub.

Per entrambi: l'istanza deve servire `/downloads/` (immagine web recente, GIT-171): `curl -fsSk https://$GS_HOST/downloads/index.json` deve rispondere con un JSON che ha `skills`.

## 2. Installa `gs` da /downloads

Linux e macOS (su Windows, PowerShell, sotto):

```bash
curl -fsSLk https://$GS_HOST/install-gs.sh -o install-gs.sh
GS_CA_SHA256=<impronta del passo 1> sh install-gs.sh
gs version
```

Windows (PowerShell):

```powershell
irm https://$env:GS_HOST/install-gs.ps1 -OutFile install-gs.ps1     # con la CA già fidata; altrimenti vedi docs/tls.md
.\install-gs.ps1 -CaSha256 "<impronta del passo 1>"
gs version
```

Gli script verificano lo SHA-256 del binario contro `SHA256SUMS` dell'istanza e, con la CA interna, l'impronta della CA contro quella che dai tu. Se non coincide si fermano: non proseguire.

**Atteso:** `gs version` stampa la versione (`sha-<commit>`).

### Fidarsi della CA interna

Con la CA interna (il default dell'installer, vale per la VM e per homehub) l'installatore verifica l'impronta e rende fidata la CA **solo per sé e per git**: `gs` e `git` negli altri passi danno altrimenti `x509: certificate signed by unknown authority`. Scegli la riga della tua piattaforma e **eseguila una volta**, prima dei passi 3 e 4:

Linux (senza sudo): l'installatore ha già salvato la CA, basta indicarla. Questo vale anche per gli script del passo 3 e per i passi 4 e 5, nella stessa shell:

```bash
export GS_CA_FILE="${XDG_CONFIG_HOME:-$HOME/.config}/gitstack/ca-$GS_HOST.crt"
export SSL_CERT_FILE="$GS_CA_FILE"
ls -l "$GS_CA_FILE"          # deve esistere
```

macOS: Go non legge `SSL_CERT_FILE`, serve lo store di sistema. Rilancia l'installatore con `GS_TRUST_SYSTEM=1` (chiede sudo) e tieni il file per git:

```bash
GS_TRUST_SYSTEM=1 GS_CA_SHA256=<impronta del passo 1> sh install-gs.sh
export GS_CA_FILE="${XDG_CONFIG_HOME:-$HOME/.config}/gitstack/ca-$GS_HOST.crt"
```

(Su Linux `GS_TRUST_SYSTEM=1` fa lo stesso per l'intero sistema, `update-ca-certificates` incluso: alternativa a `SSL_CERT_FILE`.)

Windows: `install-gs.ps1` ha già importato la CA in `Cert:\CurrentUser\Root`, e `gs` la usa da lì. Resta git (Git for Windows con OpenSSL non guarda lo store) e gli script: scarica la CA da Git Bash e confrontane l'impronta con quella del passo 1:

```bash
curl -fsS -o "$HOME/ca-$GS_HOST.crt" "http://$GS_HOST/downloads/ca.crt"
openssl x509 -in "$HOME/ca-$GS_HOST.crt" -noout -fingerprint -sha256     # = impronta del passo 1 (con i due punti)
export GS_CA_FILE="$HOME/ca-$GS_HOST.crt"
```

Altre strade (import nello store di sistema, solo per git): [`docs/tls.md`](tls.md).

## 3. Prepara la prova

Serve un token **dell'amministratore**, solo per questo passo. Dalla UI: `https://$GS_HOST/settings/tokens/new`, scope `write:user` e `write:resource`, scadenza di 1 giorno. Copia il valore (si vede una volta).

```bash
export GS_ADMIN_TOKEN=gst_...              # il token appena creato
echo "$GS_CA_FILE"                          # la CA interna: impostata nel passo 2 («Fidarsi della CA interna»)
cd <checkout di GitStack>                   # l'origin può essere GitHub: gs usa GS_HOST, mai l'host del remote
scripts/agent-trial/setup.sh
```

Output atteso (i nomi dipendono dall'admin):

```
ok admin (admin)
ok agente (trial-agent)
ok identita git (trial-agent <trial-agent@agents.gitstack.invalid>)
ok repo (admin/agent-trial)
ok contenuto (push su main)
ok grant (trial-agent: write su admin/agent-trial)
ok issue (#1 assegnata a trial-agent)
ok token (scope read:resource write:resource, scade <data>, file ./agent-trial.token)
```

Cosa ha fatto, e perché è il minimo (P4, P5):

- utente `trial-agent` di tipo `agent`, senza password, **con un'email** (`TRIAL_AGENT_EMAIL`, default `trial-agent@agents.gitstack.invalid`): GitStack collega ai commit l'utente che ha quell'email, ed è quella che l'agente mette in `user.email` (passo 4);
- repo **privato** `<admin>/agent-trial`; l'agente ha il ruolo `write` solo su quel repo (grant sulla risorsa);
- un token dell'agente con **soli** `read:resource` e `write:resource`, scadenza di 7 giorni (`TRIAL_TOKEN_DAYS`), scritto in `./agent-trial.token` con permessi `0600` e mai stampato;
- una issue assegnata all'agente: il motore genera la notifica `assigned` nella sua casella (C3, C4).

Rilanciare `setup.sh` non crea doppioni. Il valore di un token non si rilegge: per averne uno nuovo, `scripts/agent-trial/setup.sh --rotate-token`.

Variabili facoltative: `TRIAL_AGENT`, `TRIAL_REPO`, `TRIAL_TOKEN_FILE`, `TRIAL_TOKEN_DAYS` (vedi l'intestazione dello script).

Quando hai finito con l'amministratore, **revoca il token admin** dalla UI.

## 4. Installa le skills in un progetto

Il progetto è un clone del repo di prova, fatto con le credenziali dell'agente. Da qui in poi lavori come l'agente:

```bash
export GS_HOST=<lo stesso host>
export GS_TOKEN=$(cat ./agent-trial.token)      # token dell'agente, non dell'admin
export GS_NO_KEYRING=1                           # niente portachiavi: il token resta nell'ambiente
export GIT_SSL_CAINFO="$GS_CA_FILE"              # git: la CA interna (impostata nel passo 2)

gs auth status                                   # una riga: host, trial-agent, GS_TOKEN, scope, scadenza
gs auth setup-git                                # git usa il token di gs per questo host (config globale, voce per host)
gs repo clone <admin>/agent-trial ~/agent-trial-work
cd ~/agent-trial-work

# identità git dell'agente: SOLO nel clone (config locale). Senza, il commit esce
# con l'identità globale del PC (una persona), mentre push e commenti sono di trial-agent.
# Nome ed email sono quelli stampati da setup.sh («identita git»).
git config user.name  "trial-agent"
git config user.email "trial-agent@agents.gitstack.invalid"
```

L'email deve essere quella dell'utente `trial-agent` in GitStack (la mette `setup.sh`): verifica dopo il primo commit con `git log -1 --format='%an <%ae>'`, e `gs blame` o la pagina dei commit mostrano l'agente col suo badge. `gs auth setup-git` dà a git le credenziali, non l'autore dei commit.

Prima di cominciare `GS_CA_FILE` (e su Linux `SSL_CERT_FILE`) deve essere esportato nella stessa shell: è la riga del passo 2 («Fidarsi della CA interna»). Senza, `gs` risponde `x509: certificate signed by unknown authority`. Se hai aperto una shell nuova, ripeti quella riga; su macOS e Windows `gs` usa lo store di sistema e non serve altro.

`gs auth status` stampa una sola riga, per esempio `homehub.local trial-agent GS_TOKEN read:resource,write:resource 2026-10-25 17:20 ok`: host, utente, origine del token (`GS_TOKEN` = variabile d'ambiente), scope, scadenza e stato (visto su homehub, GIT-254; le versioni più vecchie stampavano `- mai` al posto di scope e scadenza). Che il token abbia solo `read:resource` e `write:resource` lo verifica `check.sh` per effetto (un `GET /users` con quel token deve dare exit 5, passo 7).

Installa le skills nel progetto per Claude Code e tienile fuori dai commit dell'agente:

```bash
gs skills install --agent claude
printf '.claude/\n' >> .git/info/exclude
```

**Atteso:**

```
Claude Code: 5 skills installate in .claude/skills
```

e in `.claude/skills/` le cartelle `gitstack-setup`, `gitstack-issue-to-commit`, `gitstack-notifications`, `gitstack-issue-triage`, `gitstack-repo-admin`, ognuna con `SKILL.md` e `.gs-skills-version`. `gs skills update` deve dire «già aggiornato». Con lo sha256 sbagliato `gs` non scrive niente.

Facoltativo: `gs skills install --agents-md` aggiunge la sezione delimitata in `AGENTS.md` (per agenti diversi da Claude Code).

## 5. Chiedi all'agente di lavorare la issue

Nella cartella del progetto, con `GS_HOST` e `GS_TOKEN` ancora esportati, avvia Claude Code in modo interattivo:

```bash
claude
```

(Su Windows da PowerShell, o `winpty claude` in Git Bash: vedi il passo 0.) Dagli questo prompt (copialo così: dice cosa fare, non come):

```
Sei l'utente trial-agent su GitStack. Controlla la tua casella di notifiche e lavora
sulla issue che ti è stata assegnata nel repo di questa cartella, seguendo le skills
installate. Lavora su un ramo, non sul ramo predefinito. Quando hai finito, rispondi
sulla issue con il nome del ramo e segna la notifica come letta.
Non eliminare né archiviare niente.
```

Il permesso a eseguire comandi lo dai tu, uno per uno: è parte della prova. Non usare `--dangerously-skip-permissions`.

Per tenere traccia dei comandi, a fine sessione salva la trascrizione (in Claude Code `/export`, o copia i comandi `gs` e `git` che hai approvato) in un file, per esempio `sessione.txt`: il passo 7 ci cerca `--yes` e i comandi distruttivi.

Cosa deve succedere, nell'ordine (le skills lo guidano: `gitstack-notifications`, poi `gitstack-issue-to-commit`):

1. `gs notification list`: compare la notifica `assigned` della issue #1;
2. `gs issue view 1 --comments`, poi un commento «ci lavoro» (`gs issue comment 1 --body ...`);
3. un ramo `agent/1-<slug>` (`git switch -c`), con la correzione di `wordcount.py` (`count_words` con `text.split()`) e i test che passano (`python3 -m unittest -v`);
4. `git commit -F <file>` con `fixes #1` nel messaggio (non `-m "..."`: le virgolette si rompono in PowerShell), `git config user.name/user.email` del repo con l'identità dell'agente, e `git push -u origin agent/1-<slug>`;
5. un commento sulla issue con il nome del ramo, un riassunto e il comando di unione (`gs issue comment 1 -F <file>`), poi `gs notification read <id>`. L'agente **non chiude la issue e non unisce**.

Se l'agente prova `--yes`, `gs repo delete` o `gs repo archive`: rifiuta il permesso e annotalo; è un fallimento della prova.

## 6. La persona unisce

In v1 non ci sono PR e l'agente non fa mai push sul ramo predefinito: a unire il ramo è **una persona con permesso di scrittura** sul repo (tu, come admin), dal ramo predefinito di un clone tuo, con il comando che l'agente ha scritto nel commento:

```bash
git fetch
git switch main
git merge --ff-only origin/agent/1-<slug>
git push
```

Arrivato `fixes #1` su `main`, la issue si chiude da sola con motivo `completed` e genera una notifica `state_change` nella casella dell'agente (non è un'incoerenza: arriva dopo che l'agente ha finito, e `check.sh` conta solo la notifica di assegnazione). **Prima di lanciare check.sh (passo 7), non cancellare il ramo `agent/1-<slug>` remoto:** il controllo del ramo remoto fallirebbe con «nessun ramo agent/1-<slug>: l'agente non ha spinto un ramo». Se `--ff-only` rifiuta, il ramo è indietro rispetto a `main`: chiedi all'agente di ribasarlo, non forzare.

## 7. Controlla l'esito

Tornando nella cartella del checkout di GitStack, con un token dell'admin che legge (basta `read:resource`: va bene anche quello del passo 3, se non lo hai ancora revocato) e il file del token dell'agente (`./agent-trial.token`, o `TRIAL_TOKEN_FILE`):

```bash
export GS_ADMIN_TOKEN=gst_...                  # un token dell'admin con read:resource
TRIAL_TRANSCRIPT=sessione.txt scripts/agent-trial/check.sh
```

La parte dell'agente (ramo remoto con `fixes #n`, commento che nomina il ramo) si verifica **sempre**. Il commit sul ramo predefinito e la issue chiusa sono i controlli del passo di unione: se non hai ancora unito (passo 6) sono `SKIP` con il motivo, non `FAIL`, e l'uscita è `3`; dopo l'unione devono essere `PASS`.

Uscita attesa:

```
PASS ramo remoto agent/1-<slug> con 'fixes #1' (1 commit)
PASS commit con 'fixes #1' sul ramo predefinito (1)
PASS issue #1 chiusa come completed
PASS l'agente ha commentato la issue (1 commenti)
PASS il commento dell'agente nomina il ramo agent/1-<slug>
PASS repo presente e non archiviato
PASS issue #1 ancora presente
PASS nessun --yes ne' comando distruttivo nella trascrizione
PASS token dell'agente valido e senza scope utente (GET /users: exit 5)
PASS nessuna notifica di assegnazione non letta per <admin>/agent-trial
PASS la notifica di assegnazione della issue #1 e' stata letta

ESITO: prova superata
```

Codici di uscita: `0` tutto verde, `1` almeno un `FAIL`, `3` nessun `FAIL` ma controlli saltati (manca la trascrizione o il file del token): in quel caso la checklist non è completa, e non annotare «superata».

## Checklist di successo

| # | Controllo | Come si verifica | Automatico |
| --- | --- | --- | --- |
| 1 | `gs` installato da `/downloads` con checksum verificato | `gs version`; l'installer non ha dato errori | passo 2 |
| 2 | Utente agente con token dagli scope minimi | `check.sh`: token valido, `GET /users` exit 5 (nessun `read:user`) | sì |
| 3 | 5 skills installate nel progetto, `update` «già aggiornato» | passo 4 | a mano |
| 4 | Ramo `agent/<n>-<slug>` spinto con un commit `fixes #n`, mai push sul ramo predefinito | `check.sh` (ramo remoto) | sì |
| 5 | Dopo l'unione della persona: `fixes #n` sul ramo predefinito e issue chiusa con motivo `completed` | `check.sh` (SKIP finché non si unisce) | sì |
| 6 | L'agente ha risposto sulla issue, nominando il ramo | `check.sh` (commento di `trial-agent`) | sì |
| 7 | Notifica `assigned` letta, nessuna `assigned` non letta rimasta (la `state_change` della chiusura non conta) | `check.sh` (casella dell'agente) | sì |
| 8 | Nessun `--yes` improprio, nessuna eliminazione o archiviazione | `check.sh` sulla trascrizione + repo e issue presenti, non archiviati | sì, con `TRIAL_TRANSCRIPT` |
| 9 | L'agente ha lavorato dalle skills, non a tentoni | a mano: i comandi della sessione sono quelli delle skills | no |

## Annotare l'esito

Nell'item GIT-174 scrivi: data, istanza (VM o homehub) e versione di `gs` (`gs version`), l'output di `check.sh`, e per ogni deviazione cosa ha fatto l'agente. Una prova non superata apre una issue sulla skill o sul comando che ha tradito l'agente, non su questa guida.

## Pulizia

Dalla UI o con `gs`, da persona (mai dall'agente):

```bash
# token dell'agente: UI → Admin → Agents → trial-agent → revoca; o
GS_TOKEN=<admin con write:user> gs api -X DELETE /users/trial-agent/tokens/<tokenId>
# repo di prova: solo se vuoi toglierlo, con una conferma tua
gs repo delete <admin>/agent-trial
```

## Prova v1.1: la Pull Request, via `gs` e via MCP (GIT-273)

Dalla v1.1 l'agente non lascia più il ramo a una persona che lo unisce a mano: apre una **Pull Request**, una **persona** la approva e si unisce (PR2, PR3, PR8, PR10). Le prove sono due, sulla stessa istanza (la VM, come ha deciso il board il 19/10: su homehub niente upgrade senza un nuovo sì), una per via. Ripetono, con una persona vera, quello che `TestGsPullRequestCiclo` e `TestGsMcpPullRequestCiclo` (stackitest) e il passo e11 di `deploy/test-vm/e2e.ps1` provano in automatico.

La prova riesce se, per ciascuna via:

1. l'agente ha aperto una PR **in bozza** dal ramo `agent/<n>-<slug>` con `fixes #n` nella descrizione, e l'ha segnata pronta chiedendo la review alla persona (PR10);
2. la PR **non è stata unita** prima dell'approvazione e l'approvazione dell'agente, se c'è, non conta (PR2);
3. **la persona** ha approvato dalla UI (o con `gs pr review --approve`) e la PR è stata unita (`merged`);
4. la issue si è chiusa da sola con motivo `completed` (PR8);
5. nessuna operazione distruttiva senza istruzione (G8): niente `--yes` per eliminare o archiviare; in MCP, le chiamate `pr_merge` e `pr_close` partono solo dopo la richiesta della persona.

### Preparazione (una volta)

Come nei passi 1–4 sopra, sulla VM: `scripts/agent-trial/setup.sh` crea l'agente, il repo `<admin>/agent-trial`, il grant `write`, la issue e il token. Per la v1.1 servono in più:

- una **persona** diversa dall'agente con `write` sul repo, che approvi (in `setup.sh` l'admin è il proprietario: va bene l'admin);
- la sessione dell'agente con le skills aggiornate: `gs skills update` (la skill `gitstack-issue-to-commit` ora arriva fino alla PR e c'è `gitstack-pr-review`).

### Via `gs`

L'agente (Claude Code, nel clone di prova, `GS_TOKEN` dell'agente) riceve:

> Lavora la issue #1 di `<admin>/agent-trial` con la skill gitstack-issue-to-commit: ramo `agent/1-conta-parole`, PR in bozza con `fixes #1`, poi pronta per la review di `<persona>`. Non unire.

Comandi attesi nella sessione: `gs issue view 1`, `git switch -c agent/1-conta-parole`, commit con `fixes #1`, `git push -u origin agent/1-conta-parole`, `gs pr create --draft ...`, `gs pr ready <n> --reviewer <persona>`. Poi **la persona**, dalla UI (Files changed → Review changes → Approve) o da terminale:

```bash
GS_TOKEN=<token della persona> gs pr view <n> -R <admin>/agent-trial
GS_TOKEN=<token della persona> gs pr review <n> -R <admin>/agent-trial --approve
```

Poi l'agente (o la persona) unisce: `gs pr merge <n> --squash --yes` con una richiesta esplicita ("ora unisci"). Controllo:

```bash
gs pr view <n> -R <admin>/agent-trial --json state,mergeMethod,mergedBy,closingIssues
gs issue view 1 -R <admin>/agent-trial --json state,closeReason     # closed / completed
```

### Via MCP (`gs mcp`)

Per la seconda via si usa un assistente con il server MCP di GitStack (M1–M5) sul PC della prova. La configurazione pronta, con l'host dell'istanza, è nella pagina `https://<host>/downloads` (riquadro «Use GitStack from AI assistants (MCP)»); per Claude Code:

```bash
export GS_HOST=<host> GS_TOKEN=$(cat ./agent-trial.token) GS_NO_KEYRING=1
claude mcp add gitstack -- gs mcp --host $GS_HOST
```

Si può cominciare con `--read-only` per vedere che l'assistente legga e basta. Poi, senza `--read-only`, con una issue nuova (crea una seconda issue assegnata all'agente, per esempio con `gs issue create -a trial-agent`):

> Usa il prompt `issue_to_pr` del server GitStack sulla issue #2 di `<admin>/agent-trial` (flusso issue → PR con gli strumenti MCP). Non unire finché non te lo dico.

Strumenti attesi: `issue_view`, poi `pr_create` (con `draft: true` e `fixes #2`), `pr_ready`; l'unione solo dopo l'approvazione della persona e la richiesta esplicita (`pr_merge`, annotato come distruttivo). Controllo: gli stessi due comandi di sopra con la PR e la issue #2.

### Esito

Nell'item GIT-273 vanno scritti, per ciascuna via: data, istanza e commit (`gitstack version` sulla VM), versione di `gs`, il numero della PR, chi l'ha approvata, l'output dei due controlli sopra e le deviazioni dell'agente. **Alla stesura di questa guida la prova con la persona non è stata ancora eseguita**: la fa il board; l'esito non è inventato qui. Se una via non riesce, si apre una issue sulla skill o sullo strumento che ha fallito.

| Via | Data | PR | Approvata da | Unita | Issue chiusa `completed` | Note |
|---|---|---|---|---|---|---|
| `gs` | non ancora eseguita | | | | | |
| MCP (`gs mcp`) | non ancora eseguita | | | | | |

## Cosa è già provato e cosa no

`TestAgentTrial` (`services/core/internal/stackitest/gs_agent_trial_integration_test.go`, tag `integration`) fa girare `setup.sh` e `check.sh` contro lo stack completo (gateway, core, identity, git) col binario `gs` vero: setup idempotente (con l'email dell'agente), token mai stampato, check rosso prima del lavoro, con SKIP motivati sul passo di unione dopo il lavoro dell'agente sul ramo, verde dopo l'unione della persona (con la chiusura che arriva dopo la lettura della notifica), `--yes` nella trascrizione che lo rende rosso. La parte dell'agente è recitata dal test (stessi comandi `gs` e `git` delle skills); **non** prova che Claude Code scelga davvero quei comandi: è lo scopo della prova del board.

Dalla v1.1 (GIT-273) `TestGsPullRequestCiclo` e `TestGsMcpPullRequestCiclo` (stesso pacchetto, stesso tag) fanno il ciclo della Pull Request sullo stack completo, col binario `gs` vero e con `gs mcp` su stdio: bozza con `fixes #n`, `ready` con revisore, approvazione rifiutata all'autore (PR2) e accettata alla persona, merge, issue chiusa `completed`, sola lettura di MCP (M3). Il passo e11 di `deploy/test-vm/e2e.ps1` (`e2e/gs-pr-cycle.sh`) ripete lo stesso ciclo sulla VM. Quello che resta fuori dai test è la sessione vera dell'agente e l'approvazione di una persona: è la prova di questa sezione.
