---
{"area":"requirements","id":"DOC-3e900450-a39c-42b0-a7e1-d6108b0f4363","related":["TOP-de29e79d-90cb-4163-a1fe-2978bed63f8d","OBJ-ece89711-7a10-4f38-9aad-9106ddeeeeb1","OBJ-a9f00bcc-bbe6-4a91-bc24-963bae228761","OBJ-990d3f4a-a762-48d5-a0a1-4147fca8b7d1","OBJ-a9706e40-6490-451b-a667-475fd17886f5","DOC-258621ff-aafe-4a28-8a49-b7e1c152530e","DOC-d1718775-770d-4065-8b27-3dc7a400dad6","DOC-4a4d6270-cd1a-4748-a1ad-9488cee4ad9e","DOC-6878277b-230e-448b-a761-215e9d233c54","DOC-3d5a5862-5bff-4e92-821c-c5caa78ca358","DOC-7de1b0e3-a859-4ec8-90eb-f05cdec30ab7"],"reopen_reason":"Regola su eliminazione del repo e app confermata dall'operatore il 2026-10-18 (scelta A).","schema_version":1,"sources":[],"tags":["v3","hosting","app","deploy","k3s"],"title":"v3 — Hosting di app e container: regole di prodotto","updated":"2026-10-18T17:00:34.035795100+00:00"}
---


# v3 — Hosting di app e container: regole di prodotto

## Context

Regole confermate dall'operatore il 2026-10-18 nel tema di analisi "v3 — Hosting di app e container", aperto dalle otto domande del CEO. Analisi in anticipo: la realizzazione resta **later**, dopo v1.0, v1.1 e v2, in tre passi: **v3-a App interne**, **v3-b Dati**, **v3-c App pubbliche**. Si appoggiano a [[knowledge/topics/ci-registry-release]] (CI2, CI4, CI7, RG1, RG2), [[knowledge/topics/identita-e-sicurezza]] (P3, P4), [[knowledge/topics/repository-git]] (R2, R10), [[knowledge/topics/pull-request]] (PR4) e [[knowledge/topics/installazione-e-deploy]] (N3, N5, N9, D19). Restano in «Oltre»: database gestiti, multi-nodo.

## Confirmed decisions

### Cos'è un'app

| # | Regola |
|---|---|
| H5 | **L'app appartiene a un repo:** un repo può avere più app (es. `web`, `worker`) nella scheda **Apps**. Permessi del repo, senza ruoli nuovi: `read` vede stato e storico dei deploy; `write` fa deploy, riavvio, rollback, scala e legge i log; `admin` crea ed elimina l'app, gestisce variabili, segreti, volumi e domini e la rende pubblica. Le quote contano sull'owner del repo. **Archiviazione del repo (R10):** le app si fermano e tornano avviabili alla riattivazione. **Eliminazione del repo (R2):** le app si fermano subito, tornano se il repo è ripristinato entro 7 giorni, poi sono eliminate insieme ai volumi. Un'app con immagine esterna richiede un repo, anche vuoto. |
| H7 | **Sorgente del deploy:** deploy a mano da chi ha `write` (UI, API, `gs app deploy`) scegliendo immagine e tag, dal registry del repo o esterna; un workflow può farlo con il token del job. Un'app può **seguire un branch**: quando la CI pubblica una nuova immagine per quel branch, GitStack la mette in produzione; se l'avvio fallisce resta la versione precedente. Il deploy automatico usa i permessi di chi ha fatto il push (CI4). Niente anteprime per PR nella v3. |
| H8 | **Configurazione in GitStack:** immagine, porta, variabili, segreti, volumi e limiti da UI, API, `gs app`, non da file del repo. Ogni modifica crea una **nuova versione** (immagine + configurazione); il rollback torna a una versione precedente. Variabili leggibili con `write`; segreti come CI4 (cifrati, mai riletti, mascherati nei log, gestiti da `admin`) ma **separati da quelli della CI**. |

### Esposizione

| # | Regola |
|---|---|
| H1 | **Interne di default, pubbliche su richiesta:** ogni app nasce interna (raggiungibile solo da chi raggiunge l'istanza). Diventa pubblica su internet solo se l'amministratore dell'installazione ha abilitato le app pubbliche **e** chi ha `admin` sull'app lo sceglie. P2 e P3 restano invariate: riguardano i dati di GitStack, non le app. |
| H2 | **Indirizzi e certificati:** indirizzo automatico sotto il dominio wildcard delle app (es. `sito-acme.apps.azienda.it`), scelto all'installazione (`--apps-domain` facoltativo; senza, default con avviso); consigliato un dominio separato da quello di GitStack, ammesso un sottodominio. Domini propri aggiunti da `admin`, attivi solo dopo il controllo che il DNS punti all'istanza. CA interna per le app interne, Let's Encrypt automatico via Traefik per le pubbliche (richiede la porta 80 raggiungibile da internet), oppure certificato del cliente. |
| H6 | **Login davanti alle app interne:** modo **login GitStack** (default) o **senza login** (scelta esplicita di `admin`). Con il login passa chi ha `read` sul repo (repo interno → tutti gli utenti, privato → chi ha un ruolo). L'app riceve l'utente (nome, email, team) in intestazioni firmate; gli agenti passano con un token con `read`. Le app pubbliche gestiscono da sé l'eventuale autenticazione. |

### Dati e backup

| # | Regola |
|---|---|
| H3 | **Volumi persistenti, niente database gestiti:** uno o più volumi di dimensione fissa per app, nella quota dell'owner; un'app con volume gira in una sola copia. Volumi **inclusi di default nei backup**, escludibili per app. Copia con l'app accesa: per un database in un container la coerenza non è garantita (avviso nella UI, guida al dump). |
| H4 | **Immagini in uso nei backup:** il backup include sempre le immagini in uso dalle app (versione attiva e precedente); il resto del registry resta escluso (RG2). Dopo un ripristino le app ripartono subito con i loro dati. |

### Sicurezza e risorse

| # | Regola |
|---|---|
| H9 | **Isolamento:** barriere fisse come CI2: spazio separato per app; niente privilegi, niente accesso a dischi o rete del server; nessun accesso ai servizi interni di GitStack né alle app di altri repo; comunicazione solo tra app dello stesso repo; uscita verso internet via proxy se configurato (N9). Root nel container **permesso di default** («baseline», avviso nella UI); **«restricted»** imponibile dall'amministratore per l'istanza o da `admin` per l'app. |
| H10 | **Firme al deploy:** la CI firma automaticamente con cosign le immagini che pubblica (chiave dell'istanza). Il deploy automatico accetta solo immagini firmate dalla CI dello stesso repo; il deploy a mano accetta anche immagini non firmate, etichettate «non verificata»; l'amministratore può imporre «solo immagini firmate» con chiavi fidate aggiuntive. |
| H11 | **Risorse su un nodo:** priorità servizi di GitStack > app > job della CI; parte del server riservata alle app (calcolata dall'hardware, modificabile); quote per owner su CPU, RAM e disco dei volumi con avviso all'80%; per app una **parte garantita** e un **tetto** (default piccoli, es. 0,5 vCPU / 512 MB; massimo dall'amministratore). Deploy rifiutato con messaggio chiaro solo se le parti garantite non ci stanno; sotto pressione si rallenta e poi riavvia chi supera la parte garantita. Console con garantito, uso e tetto per app. |

### Operatività e agenti

| # | Regola |
|---|---|
| H12 | **Operatività:** log in diretta più cronologia (indicativamente 7 giorni o 1 GB per app, regolabile); metriche minime (CPU, RAM, riavvii; richieste ed errori per le app web); controllo di salute che decide quando una versione è partita; riavvio e rollback alle ultime versioni (la precedente sempre disponibile, le più vecchie se l'immagine è ancora nel registry); notifiche a chi ha fatto il deploy se l'app si ferma o il deploy fallisce; webhook `deployment`. **Scala solo a mano** entro la quota; con due o più copie aggiornamento senza interruzione. |
| H13 | **Agenti:** stessa API per UI, `gs app …` (`--json`) e strumenti MCP; un agente con `write` fa deploy, riavvio e rollback da solo; l'amministratore lo limita con i grant (P4). Operazioni da `admin` con annotazione «distruttiva» in MCP. Freno consigliato: «Richiedi una Pull Request» su `main` (PR4). Deploy dell'agente nello storico con la sua identità; notifica ad `admin` se fallisce. Skill con il giro deploy → salute → log → rollback. |

## Fuori dalla v3

Database gestiti e multi-nodo («Oltre»); anteprime per ogni PR, definizione dell'app in un file del repo, scala automatica (idee nel Dream Journal).

## Related topics

- [[knowledge/topics/ci-registry-release]]
- [[knowledge/topics/installazione-e-deploy]]
- [[knowledge/topics/repository-git]]
- [[knowledge/topics/visione]]

