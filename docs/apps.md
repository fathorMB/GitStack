# Applicazioni ospitate

Le applicazioni sono configurate in GitStack: ogni modifica di immagine, porta, variabili o segreti crea una nuova versione e avvia un deploy. Le variabili sono visibili a chi ha `write`; i segreti sono gestiti solo da chi ha `admin` e il loro valore non viene mai restituito dalle API.

## Deploy automatico da branch o da regola sui tag, e firme (H7, H10)

**Firma.** La CI firma con cosign, con la chiave dell'istanza, ogni immagine che pubblica con il token del job; la chiave pubblica sta in `GET /registry/signing-key` e, per l'amministratore, in cima a `GET /admin/apps/trusted-keys`. Una versione dell'app porta `image.verified` (e `image.signedBy`): vero solo se la firma è valida. La firma della CI vale solo per **questo** repo e per **questa** immagine: la firma ricopiata da un altro repo o da un'altra immagine non conta (il riferimento firmato deve coincidere con `owner/repo[/immagine]`; se il repo viene rinominato le immagini già pubblicate risultano non verificate finché la CI non le ripubblica).

**Seguire un branch.** Con `followBranch` l'app segue un branch: quando un job della CI del repo, lanciato da un push (o da `workflow_dispatch`) su quel branch, pubblica un tag della stessa immagine dell'app, GitStack mette in produzione la nuova versione (`trigger: branch_push`, `branch`, `commitSha`). Il deploy usa i permessi di chi ha fatto il push (CI4): se non ha più `write` sul repo non succede niente. Se l'avvio fallisce resta la versione precedente (H12). Non scatta, e il perché va nel log di core:

- per un'immagine non firmata, firmata da un altro repo o da una chiave fidata aggiuntiva: il deploy automatico accetta **solo** la firma della CI di questo stesso repo, e `followBranch` su un'app con un'immagine così è rifiutato (422 `follow_branch_unsigned`);
- se l'app ha il deploy automatico sospeso (`autoDeployPaused`), per esempio dopo un rollback;
- se l'immagine è già quella dell'ultima versione (altro tag dello stesso build) o se c'è un altro deploy in corso: il prossimo push riprova.

**Seguire una regola sui tag.** In alternativa al branch l'app segue una regola sui tag: `followTagPattern` (per esempio `v*-test.*`, `gs app create --follow-tag 'v*-test.*'`). Quando nel registry del repo arriva, pubblicata da un job della CI (push o `workflow_dispatch`), un'immagine della stessa app con un tag che rispetta la regola, GitStack la mette in produzione (`trigger: tag_push`, `commitSha` dall'immagine). La regola è un glob (`*` qualsiasi sequenza, `?` un carattere, `[a-z]` una classe) e deve combaciare con **tutto** il tag: con `v*-test.*` l'immagine firmata `v1.2-test.3` va in produzione, `v1.2` no. Valgono le stesse condizioni del branch: immagine firmata **dalla CI di questo repo** (mai non firmata, mai firmata da un altro repo o da una chiave aggiuntiva), permessi di chi ha fatto il push (CI4; su un repo in mirror in pull è chi ha acceso la CI sul mirror, CI3), se l'avvio fallisce resta la versione precedente (H12), niente deploy se `autoDeployPaused`.

**Una sola sorgente.** Un'app segue un branch **oppure** una regola sui tag, mai tutte e due: `followBranch` e `followTagPattern` insieme, alla creazione o alla modifica, danno 422 `follow_both`. Per passare dall'una all'altra si toglie la prima nella stessa richiesta (`PATCH` con `{"followBranch": null, "followTagPattern": "v*-test.*"}`). Anche `followTagPattern` richiede un'immagine del registry firmata dalla CI (422 `follow_branch_unsigned`) e una regola valida (422 sul campo).

**Un tag preciso dalla CI.** Un workflow con il token del job (vedi sotto) mette in produzione un tag preciso senza regola né branch: `gs app deploy web --image v1.2-test.3` con `GS_TOKEN` uguale al token del job, oppure `POST /repos/{owner}/{repo}/apps/{app}/deploys` con `{"image": "v1.2-test.3"}`. La versione ha `trigger: manual` e `commitSha` letto dall'immagine (H14), come il deploy a mano.

**A mano.** Il deploy a mano (UI, API, `gs app deploy`) accetta anche immagini non firmate o esterne, etichettate «non verificata». Anche un workflow lo fa con il token del job (`Authorization: Bearer $GITSTACK_TOKEN` del job) su `POST /repos/{owner}/{repo}/apps/{app}/deploys`: serve `permissions: packages: write`, vale solo per le app del proprio repo ed è solo il deploy, nient'altro; il deploy è a nome di chi ha causato il run, che deve avere ancora `write` (un run senza autore, come una schedule, non può).

**Solo immagini firmate.** L'amministratore può accendere `signedImagesOnly` (`PUT /admin/apps/settings`): da quel momento creazione, deploy a mano e rollback accettano solo immagini firmate dalla CI del repo o da una **chiave fidata aggiuntiva**, altrimenti 422 `image_not_allowed` (un'immagine esterna non si può verificare, quindi è rifiutata). Le chiavi aggiuntive sono chiavi pubbliche cosign ECDSA in PEM (`POST /admin/apps/trusted-keys`; 422 `invalid_key`, 409 `key_exists`); si tolgono con `DELETE` (la chiave della CI dell'istanza no: 409 `instance_key`). Le app già in esecuzione non si fermano; i deploy successivi usano le chiavi di quel momento.

## Segreti e rollback

Un Secret Kubernetes dell'app contiene i valori correnti dei soli nomi elencati dalla versione in esecuzione. Il Deployment lo riferisce con `envFrom`: i valori non compaiono nello spec del Deployment.

Le versioni conservano i nomi dei segreti, non i loro valori. Un redeploy o un rollback ripristina immagine, variabili e nomi dei segreti della versione scelta, ma legge sempre il valore corrente di ciascun segreto. Per questo la rotazione di un segreto resta valida anche dopo un rollback.

Se una versione nomina un segreto eliminato, GitStack rifiuta il rollback o il redeploy con `409 app_secrets_missing`, indicando i nomi mancanti. Non cambia la versione attiva e non aggiorna il Secret Kubernetes.

## Raggiungere un'altra app del repo per nome (H15)

Dentro lo stesso repo ogni app si raggiunge con il proprio nome, come indirizzo interno del cluster: dal sito, `http://api` arriva all'app `api` dello stesso repo; un'app `postgres` si raggiunge come `postgres:5432`. Non serve configurare niente: il nome è quello che si è dato all'app.

- **Porte.** Ogni app con una porta espone due porte sul proprio indirizzo interno: la **80** (la usano anche l'indirizzo automatico e il login) e la **porta dichiarata** nella configurazione dell'app, se è diversa da 80. Quindi con porta 8080 funzionano `http://api` (porta 80, la stessa app) e `http://api:8080`; un client Postgres con porta 5432 usa `postgres:5432`. Un'app senza porta dichiarata non ha né indirizzo interno né indirizzo automatico.
- **Stabile.** Il nome resta valido dopo deploy, rollback, riavvio e scala: non dipende dai pod. Si aggiorna entro qualche secondo quando un'app del repo viene creata, rinominata o eliminata.
- **Solo lo stesso repo.** Un'app di un altro repo con lo stesso nome non si risolve e non è raggiungibile: la rete lo vieta (H9). Il traffico verso internet e verso i servizi di GitStack resta chiuso come prima; per risolvere i nomi le app possono interrogare solo il DNS del cluster.
- **Nomi non pubblicati.** Un'app non raggiunge sé stessa per nome. Non ottengono un nome interno le app chiamate `app` o `kubernetes`, o con un nome che non inizia con una lettera: il motivo compare nel log dell'app che avrebbe dovuto usarlo (evento della piattaforma). Se in un namespace esiste già un Service con quel nome, non viene sovrascritto.
- **Per chi scrive l'app.** Usare il nome come host (`http://api:8080/…`): niente indirizzi IP né l'indirizzo `*.apps.<dominio>`, che passa dal login e dal TLS. Il nome non è un permesso: l'autenticazione tra app, se serve, la fa l'app.

Meccanismo e alternative scartate: ADR 0002, sezione «Nomi interni fra app dello stesso repo (H15)».

## Variabili di sistema GITSTACK_

A ogni deploy GitStack passa al container alcune variabili di sistema, che l'app può leggere per sapere che cosa sta girando:

| Variabile | Valore |
|---|---|
| `GITSTACK_APP_VERSION` | il tag dell'immagine, per esempio `sha-1a2b3c` o `v2`. Assente se l'immagine è indicata solo per digest, senza tag. |
| `GITSTACK_DEPLOYED_AT` | data e ora della versione, in RFC 3339 UTC, per esempio `2026-10-26T07:30:15Z`. Resta la stessa a ogni riavvio della versione. |
| `GITSTACK_COMMIT_SHA` | il commit dell'immagine, dall'etichetta OCI `org.opencontainers.image.revision` (se c'è). Viene letta dal registry del repo al deploy e salvata nella versione, quindi resta la stessa a ogni riavvio; una modifica di configurazione la eredita dalla versione attiva. **Assente** se l'immagine è esterna, se non ha l'etichetta o se l'etichetta non è un commit esadecimale (da 7 a 64 caratteri). |
| `GITSTACK_API_URL` | l'API di GitStack raggiunta **dentro il cluster**, per esempio `http://gitstack-gateway.gitstack.svc.cluster.local:8081/v1` (vedi [Chiamare l'API di GitStack](#chiamare-lapi-di-gitstack-dallapp-h16)). **Assente** se l'installazione non ha la porta interna del gateway (`gateway.internalPort: 0`, o app non abilitate nel chart). |
| `GITSTACK_APP_KEYS_URL` | le chiavi pubbliche Ed25519 per verificare le intestazioni firmate dal login (H6), raggiunte **dentro il cluster** sulla porta interna del gateway, per esempio `http://gitstack-gateway.gitstack.svc.cluster.local:8081/.well-known/gitstack-app-keys` (senza autenticazione; vedi [apps-login.md](apps-login.md)). Presente e assente alle stesse condizioni di `GITSTACK_API_URL`. |
| `PORT` | la porta dell'app, come prima. |

Il commit di ogni versione è leggibile nell'API delle versioni dell'app, nel campo `commitSha` (`null` se la versione non ne ha uno).

Il prefisso `GITSTACK_` è riservato: non si può creare una variabile o un segreto dell'app con quel prefisso (anche in minuscolo). L'API risponde 422 con l'errore sul campo `name`, e la pagina **Variables & secrets** mostra lo stesso messaggio prima di inviare. Un nome come `MY_GITSTACK_X` è ammesso, perché il prefisso conta solo all'inizio.

## Volumi persistenti (H3)

Un'app può avere fino a 8 volumi di dimensione fissa, ognuno con un nome e un percorso di mount
assoluto (non la radice e non sotto `/proc`, `/sys`, `/dev`). Li gestisce chi ha `admin` sul repo:

- `GET /repos/{owner}/{repo}/apps/{app}/volumes` elenca i volumi e la quota disco dell'owner;
- `POST …/volumes` con `{"name", "mountPath", "sizeBytes"}` aggiunge un volume (almeno 1 MiB);
- `DELETE …/volumes/{volume}` lo elimina **insieme ai dati**: non si annulla.

Sono PVC `ReadWriteOnce` su `local-path`, lo storage di k3s. `local-path` non applica la dimensione come
limite fisico del disco: la dimensione fissa è quella che conta nella quota disco dell'owner
(`/admin/apps/quotas`, 20 GiB di default). Oltre la quota la richiesta è rifiutata (409
`insufficient_capacity`, `details.missing.diskBytes`); dall'80% la risposta porta `quota.warning: true`.

Un'app con volumi gira in **una sola copia**: la scala oltre 1 risponde 409 `single_copy`, come
l'aggiunta di un volume a un'app che ha più copie (prima si scende a una). L'aggiornamento è di tipo
`Recreate`: il pod vecchio si ferma prima che parta il nuovo, quindi c'è una breve interruzione a ogni
deploy, riavvio o modifica. Ogni aggiunta o rimozione di un volume crea una nuova versione.

I volumi sono dati, non configurazione: un rollback ripristina immagine, variabili e nomi dei segreti ma non
toglie né rimette volumi. GitStack non fa il dump di un database: per questo resta la procedura di chi lo gestisce.

## Uscita delle app: internet aperto, rete privata chiusa (H16)

Le app escono con le stesse regole di webhook e mirror (`pkg/egress`, C8), applicate dalla NetworkPolicy `app-isolation` del namespace dell'app, quindi valgono anche per un'immagine che gira come root:

- **internet** è aperto (IPv4 e IPv6; se l'istanza ha un proxy, passa da lì);
- la **rete privata** (RFC 1918, ULA, CGNAT) è chiusa: un'app non raggiunge il database o il server di posta della tua LAN;
- **loopback, link-local, indirizzi del cluster e del nodo e porte di Kubernetes** (6443, 10250, 10255, 2379-2380) restano bloccati sempre, anche se qualcuno li elenca.

### Chiamare l'API di GitStack dall'app (H16)

Su un'installazione senza proxy l'indirizzo pubblico di GitStack si risolve sul nodo, quindi è bloccato (e l'amministratore non può aprirlo: il nodo e il cluster non si aprono mai). Per questo ogni app ha un percorso fisso verso il **gateway dentro il cluster**, e solo verso quello: mai core, identity, git, Postgres o NATS.

- il gateway apre una **porta interna** (`gateway.internalPort`, default 8081, `GITSTACK_GATEWAY_INTERNAL_ADDR`) che serve solo `/v1`: niente forward-auth e login delle app, niente pagine web. Non si fida mai di `X-Forwarded-For`: l'IP del client per rate limit e audit è quello del peer, quindi un'app non può falsificarlo;
- la NetworkPolicy dell'app ha una regola di egress verso i soli pod del gateway su quella porta (anche per un'app con l'uscita ristretta, come DNS e peer del repo), e quella del gateway ammette solo i pod dei namespace delle app, solo su quella porta. Se core non conosce il gateway (variabili `GITSTACK_CORE_APPS_GATEWAY_*` non impostate dal chart) la regola non c'è e tutto resta com'era;
- `GITSTACK_API_URL` contiene l'indirizzo, già col prefisso `/v1` del gateway (la porta interna non passa da Traefik, che toglie `/api`: `https://host/api/v1` e `GITSTACK_API_URL` sono la stessa API).

La rete apre solo il trasporto: l'autorizzazione resta quella del token. Con il token di un bot:

```sh
curl -sS -H "Authorization: Bearer $BOT_TOKEN" "$GITSTACK_API_URL/meta"
curl -sS -H "Authorization: Bearer $BOT_TOKEN" -H 'Content-Type: application/json'   -d '{"title":"Segnalazione dal tester"}' "$GITSTACK_API_URL/repos/acme/web/issues"
```

Il token (un segreto dell'app, per esempio `BOT_TOKEN`: il prefisso `GITSTACK_` è riservato) ha gli scope e i permessi del bot, nient'altro.

### Per l'amministratore: aprire una destinazione privata

Un'app che deve parlare con un servizio della LAN (per esempio `db.azienda.lan:5432` o `smtp.acme.lan:587`) non ha bisogno di una rete aperta: l'amministratore dell'installazione apre **solo quell'indirizzo e quella porta**.

1. **Console.** In *Administration → Apps & resources → Instance settings*, riga *Private destinations*, **+ Add destination**: host (IP o nome DNS), porta e una nota facoltativa. *Remove* chiude la destinazione.
2. **API.** `GET /admin/apps/egress` legge l'elenco, `PUT /admin/apps/egress` lo sostituisce per intero (`{"privateDestinations":[{"host":"db.azienda.lan","port":5432,"note":"database del backoffice"}]}`). Serve essere amministratore dell'installazione (altrimenti 403).

Cosa succede:

- l'API risponde **422 `destination_blocked`** per un indirizzo del cluster (`10.42.0.0/16`, `10.43.0.0/16`), un indirizzo del nodo, loopback, link-local, una porta di Kubernetes o una destinazione della lista `GITSTACK_EGRESS_DENY`; **422 `destination_unresolved`** se il nome non si risolve. Se un nome porta a più indirizzi basta uno vietato per rifiutarlo, e nulla viene scritto;
- la destinazione vale **solo** per l'indirizzo e la porta TCP indicati: dell'app resta chiuso tutto il resto della rete privata, e la stessa porta su un altro host;
- la NetworkPolicy lavora sugli indirizzi IP: un nome si risolve quando la policy viene applicata (a ogni deploy e a ogni modifica di queste impostazioni), non di continuo. Se l'indirizzo del servizio cambia, riapplica salvando di nuovo le impostazioni o con un deploy dell'app;
- la modifica si applica subito alle app in esecuzione, senza riavvii e senza creare versioni;
- la lista `--egress-allow` di webhook e mirror (`docs/operations.md`) non apre la rete privata alle app: per le app l'unico modo è questa console. La lista vietata `--egress-deny`, invece, vale anche qui.

### Per chi configura l'app: restringere l'uscita

Chi ha `admin` sul repo può restringere l'uscita della **sua** app a un elenco di destinazioni: *App → Settings → Volumes & resources → Outbound network → Restrict this app*. Con la restrizione l'app raggiunge solo le destinazioni elencate, più le app dello stesso repo (per nome, H15) e il DNS; niente internet aperto. L'app esce **in modo diretto** verso le destinazioni elencate, senza il proxy dell'istanza (vedi sotto).

- `GET /repos/{owner}/{repo}/apps/{app}/egress` (serve `write`) mostra la restrizione e le destinazioni private che l'amministratore ha aperto (`allowedPrivate`);
- `PUT …/egress` con `{"destinations":[{"host":"api.example.com","port":443}]}` la imposta (serve `admin`); un elenco vuoto chiude ogni altra uscita; `DELETE …/egress` la toglie e l'app torna all'uscita dell'istanza;
- una voce pubblica (internet) va sempre bene; una voce nella **rete privata** deve essere una di quelle aperte dall'amministratore, con la stessa porta, altrimenti **422 `destination_blocked`**. Una restrizione non può mai aprire più di quanto permette l'amministratore. Se l'amministratore chiude poi una destinazione, l'app la perde alla prima riapplicazione della policy (subito, perché la chiusura riapplica la policy di tutte le app) anche se resta scritta nell'elenco;
- come per l'amministratore, un nome si risolve all'applicazione della policy e vale per le porte TCP;
- **proxy dell'istanza.** Un'app con l'uscita ristretta esce in modo diretto verso le destinazioni elencate, senza il proxy dell'istanza: se la rete dell'installazione esce solo dal proxy, un'app ristretta non raggiunge internet. Per questo un'app ristretta non riceve `HTTP_PROXY`, `HTTPS_PROXY` e `NO_PROXY` (né le minuscole) dell'istanza; accendere o spegnere la restrizione cambia l'ambiente del pod e riavvia l'app (rollout, copia per copia). La scheda *Volumes & resources* mostra questo avviso accanto a *Restrict this app* quando l'istanza ha un proxy (`instanceProxy` nella risposta di `GET …/egress`).

Se l'app usa un database nello stesso repo (un'altra app, H15) non serve nessuna apertura: le app dello stesso repo si raggiungono per nome anche con la restrizione.
