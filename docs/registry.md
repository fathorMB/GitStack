# Registry OCI (v2-b, RG1)

Ogni repo può pubblicare immagini OCI su GitStack con `docker`, `podman`, `crane` o dalla CI. Regole in [c_71844eeffde33356] (RG1-RG2); questa pagina è la guida di chi lo usa e di chi lo gestisce.

## Indirizzi e login

```sh
echo "$GS_TOKEN" | docker login gitstack.example.com -u token --password-stdin
docker tag app:1 gitstack.example.com/alice/app:1             # immagine del repo alice/app
docker tag web:1 gitstack.example.com/alice/app/web:1         # immagine con un nome sotto il repo
docker push gitstack.example.com/alice/app/web:1
docker pull gitstack.example.com/alice/app/web:1
```

L'indirizzo è `<host>/<owner>/<repo>[/<nome>]`. Il registry è alla radice dell'host (`/v2/`), come lo smart HTTP di git, non sotto `/api`.

- **Utente:** qualunque, si ignora. **Password:** un token personale o di un agente con gli scope del registry, oppure il token del job (`gsj_…`, vedi sotto).
- **Nessun accesso anonimo**, nemmeno a `/v2/`: senza credenziali valide ogni richiesta è 401.
- Il registry non ha un catalogo (`/v2/_catalog` non esiste): l'elenco delle immagini di un repo è `GET /repos/{owner}/{repo}/packages`, che arriva con la scheda Packages (oggi il contratto lo dichiara ma risponde 501).

## Scope e permessi

Servono sempre **due** cose: lo scope del token e il ruolo sul repo. Vale il più stretto.

| Azione | Scope del token | Ruolo sul repo |
|---|---|---|
| scaricare (`pull`) | `read:registry` | `read` (il repo interno lo leggono tutti gli utenti attivi, P7) |
| pubblicare (`push`) | `write:registry` (include `read:registry`) | `write` |
| eliminare un manifest o un blob (`DELETE`) | `admin:registry` (include gli altri due) | `admin` |

Gli scope `*:registry` sono separati da `*:resource`: un token che legge e scrive il codice non apre il registry, e un token per `docker login` non tocca il resto. Un repo che non esiste, è eliminato o non è leggibile dal chiamante risponde sempre 404 (`NAME_UNKNOWN`), come per git. Un ruolo o uno scope insufficiente è 403 (`DENIED`) con il motivo nel messaggio.

## Token del job

Il token effimero di un job di CI (`gsj_…`, CI4) fa `docker login` e vale **solo per il repo del suo job**: scarica se il livello `packages` del job è `read` o `write`, pubblica se è `write` (il workflow lo chiede con `permissions: packages: write`, entro il tetto del repo e di chi ha avviato l'esecuzione), **non elimina mai**. Scade con il job. Il registry lo convalida nel ci-gateway di core (`GET /ci/v1/registry-access`); senza `git.registry.ciGateway` nel chart i token del job non entrano.

## Archiviazione, eliminazione, ripristino (R2, R10)

- **Archiviato:** le immagini si scaricano, ma push, eliminazioni e montaggi di blob sono 403 («repository archiviato»), per tutti, admin compresi.
- **Eliminato** (cestino, 7 giorni): le immagini spariscono con il repo (404), restano su disco o S3. **Ripristinato** entro i 7 giorni: tornano com'erano. Alla **cancellazione definitiva** il servizio git toglie i repository del registry di quel repo; i blob che nessun repo referenzia restano finché la pulizia (RG2) non li raccoglie.
- Le immagini stanno sotto l'**id** del repo, non sotto owner/nome: se il repo cambia nome le immagini lo seguono.

## Installazione e configurazione

Distribution (`github.com/distribution/distribution/v3`, licenza Apache-2.0) è usato come libreria dentro il servizio git, che vi aggiunge autenticazione e permessi. Il traffico passa dal gateway (`/v2/` → git): request id, log, rate limiting per IP, nessun timeout né limite al corpo (un livello di immagine ha centinaia di MB).

Nel chart (`deploy/gitstack/values.yaml`, `git.registry`):

| Valore | Significato |
|---|---|
| `enabled` | `true` di default; `false` spegne `/v2/` (404) |
| `ciGateway.url`, `.port` | ci-gateway di core, per i token del job; vuoto = solo token personali |
| `s3.enabled`, `.bucket`, `.region`, `.endpoint`, `.pathStyle`, `.prefix`, `.existingSecret` | archiviazione su S3 (o compatibile, per esempio MinIO) invece del disco. Chiavi nel Secret `existingSecret` (chiavi `accessKey` e `secretKey`), mai nei values |

Senza S3 i dati stanno sul PVC `git-data`, in `<dati>/registry`. Variabili d'ambiente del servizio git: `GITSTACK_GIT_REGISTRY`, `GITSTACK_GIT_REGISTRY_DIR`, `GITSTACK_GIT_CI_GATEWAY_URL`, `GITSTACK_GIT_REGISTRY_S3_*` (vedi `services/git/README.md`); del gateway: `GITSTACK_REGISTRY_URL`.

`v2` è un nome riservato (`pkg/names`): nessun utente o organizzazione può chiamarsi così.

## Limiti noti

- Le immagini sono sul PVC `git-data` e quindi nei backup del volume: «immagini escluse di default dai backup» (c_73ffb543ccfe3876) è un item successivo.
- Nessuna quota, nessun tag protetto, nessuna pulizia (RG2 e successivi).
- Il rate limit del gateway conta le richieste per IP, non per token (il registry usa Basic, non Bearer): un push di molti livelli consuma il burst dell'IP.
- Un `docker login` fuori da `localhost` richiede HTTPS: usa la CA dell'istanza (`/downloads/ca.crt`, vedi `docs/tls.md`).
