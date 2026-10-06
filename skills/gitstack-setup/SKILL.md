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

## Più istanze

L'istanza si sceglie così: `--hostname`, poi il remote `origin` del repo corrente, poi `GS_HOST`, poi quella predefinita. `GS_TOKEN` vale solo per l'istanza di `GS_HOST`.

## Regola sulle operazioni distruttive

Non usare mai `--yes` su `gs repo delete`, `gs repo archive` o altre eliminazioni e archiviazioni senza un'istruzione esplicita di una persona in questa conversazione. Senza terminale, in assenza di `--yes`, il comando esce con 2 e non fa nulla: è il comportamento voluto.
