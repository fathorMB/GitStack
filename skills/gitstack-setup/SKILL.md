---
name: gitstack-setup
description: Installa e configura la CLI gs di GitStack e fai il login su un'istanza. Usala quando gs non è installato, non è autenticato (exit 4) o serve scegliere l'istanza.
---

# GitStack: installazione e login

`gs` è la CLI di GitStack (stile `gh`). Ogni istanza serve i binari su `https://<host>/downloads/` (`index.json` elenca file e sha256).

## Installare

Scarica il binario della tua piattaforma da `https://<host>/downloads/`, verifica lo sha256 con `SHA256SUMS` e mettilo nel `PATH`. Controlla:

```
gs version
```

## Autenticarsi

Per un agente il modo più semplice sono le variabili d'ambiente, senza toccare il disco:

```
export GS_HOST=gitstack.example.com
export GS_TOKEN=<token personale>
```

Altrimenti il login salva il token (portachiavi o file solo-utente), leggendolo da stdin:

```
echo "$GS_TOKEN" | gs auth login --hostname gitstack.example.com --with-token
```

Una persona può usare `gs auth login --web`, che apre la pagina per creare un token con gli scope consigliati. Un agente ha il proprio account e il proprio token: non usare mai il token di una persona.

## Verificare

```
gs auth status
```

Mostra utente, scope e scadenza. Exit 4 = token scaduto, revocato o mancante: chiedi a una persona un token nuovo, non provare altri account.

## Git con le stesse credenziali

```
gs auth setup-git
```

Configura git perché usi il token di `gs` per clone e push sull'istanza.

## Identità git dell'agente

`gs auth setup-git` dà a git le credenziali, non l'autore dei commit: senza altro i commit escono con l'identità globale del computer (cioè di una persona), mentre push e commenti sono tuoi. Nel clone di lavoro imposta, in config locale (mai `--global`):

```
git config user.name "<il tuo username GitStack>"
git config user.email "<l'email del tuo account GitStack>"
```

L'email è quella dell'utente agente, che GitStack collega ai commit; la dà la persona che ha creato l'account (`scripts/agent-trial/setup.sh` la stampa). Se non la conosci chiedila, non usare l'identità globale. Per i messaggi di commit e i commenti lunghi usa un file (`git commit -F msg.txt`, `gs issue comment N -F comment.txt`): le virgolette di `-m "..."` si rompono in PowerShell.

## Windows

- `curl.exe` (schannel) con la CA interna dà `CERT_TRUST_REVOCATION_STATUS_UNKNOWN`: la CA non pubblica revoche, aggiungi `--ssl-no-revoke`.
- Con `GIT_CONFIG_NOSYSTEM` git.exe usa schannel e non accetta percorsi `/c/...`: scrivi il file della CA come `C:/Users/...`.

## Più istanze

L'istanza si sceglie così: `--hostname`, poi `GS_HOST`, poi il remote `origin` del repo corrente (solo se è un'istanza già configurata), poi quella predefinita. `GS_TOKEN` vale solo per l'istanza di `GS_HOST` (o la predefinita se `GS_HOST` manca): mai per un altro host, nemmeno quello del remote.

## Regola sulle operazioni distruttive

Non usare mai `--yes` su `gs repo delete`, `gs repo archive` o altre eliminazioni e archiviazioni senza un'istruzione esplicita di una persona in questa conversazione. Senza terminale, in assenza di `--yes`, il comando esce con 2 e non fa nulla: è il comportamento voluto.
