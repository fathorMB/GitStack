# ADR 0001 — CI v2-a: esecutore dei workflow e architettura

- Stato: proposto (GIT-274), da approvare dal board
- Data: 2026-10-19
- Regole di prodotto: CI1–CI7 [c_baa86c8ddb5db70d], RG2/CI7 rivisti il 18/10 per la v3
  (firma delle immagini con chiave dell'istanza; PriorityClass servizi > app > job)
- Prova di fattibilità: [`poc-ci-executor/`](poc-ci-executor/) con i log in `poc-ci-executor/logs/`

## Contesto

La v2-a porta in GitStack una CI in stile GitHub Actions. CI1 chiede di riusare un esecutore open
source se ce n'è uno adatto, dopo aver verificato la licenza rispetto ad AGPL-3.0 (licenza di
GitStack: `LICENSE` nella radice è la GNU AGPL v3). CI2 vuole job come pod usa e getta con barriere
fisse: niente privilegi, niente rete interna, build di immagini senza accesso al runtime. Il
README fissa «pochi servizi grossi»: quindi niente servizio nuovo se `core` basta.

## Licenze verificate

Letto il file `LICENSE` dell'upstream il 2026-10-18/19 (non il campo di un indice):

| Componente | Licenza (dal file) | Con AGPL-3.0 |
|---|---|---|
| nektos/act (v0.2.89, 2026-06-01) | MIT | compatibile: codice MIT può entrare in un'opera AGPL |
| gitea.com/gitea/act (fork usato dai runner Gitea/Forgejo) | MIT | come sopra |
| gitea.com/gitea/act_runner (v5.1.1, 2026-10-16) | MIT | come sopra |
| gitea.com/gitea/actions-proto-go (protocollo runner) | MIT | come sopra |
| code.forgejo.org/forgejo/runner (v13.2.0, 2026-09-18) | **GPL-3.0** | compatibile (GPLv3 §13 e AGPLv3 §13 consentono la combinazione); non serve, perché lo si usa come processo separato |
| actions/runner (runner ufficiale GitHub) | MIT | compatibile, ma non utilizzabile (sotto) |
| moby/buildkit, buildah, kaniko | Apache-2.0 | compatibile con GPLv3, quindi con AGPLv3 |

Le date vengono dall'API di rilascio dei rispettivi siti. Non è un parere legale: è la lettura
dei testi e la compatibilità nota dalle FAQ della FSF.

## Alternative valutate

**Esecutore dei workflow**

1. **nektos/act** — esegue i workflow GitHub Actions da riga di comando o come libreria Go.
   Per i job usa Docker; ha anche una modalità host (`-P <label>=-self-hosted`) che esegue i passi
   come processi nello stesso ambiente. **Scelto**, in modalità host dentro il pod (provato).
2. **Runner di Forgejo** — runner completo con protocollo a pull verso un server Forgejo, basato
   su un fork di act. GPL-3.0. Non scelto come esecutore interno: porta con sé un'identità di
   server (Forgejo) che GitStack non ha, e vuole Docker o LXC per isolare. Resta il candidato come
   **runner esterno** (vedi protocollo).
3. **act_runner di Gitea** — stesso schema di Forgejo, MIT. Stessa valutazione: runner esterno.
4. **actions/runner** — non provato; legato ai servizi di orchestrazione di GitHub. Scartato.
5. **Esecutore scritto da noi** — contro CI1; riconsiderato solo se act non regge (vedi rischi).

**Build di immagini senza privilegi (CI2)**

1. **BuildKit rootless, senza demone** (`buildctl-daemonless.sh`) — Apache-2.0, nessun socket del
   runtime. **Scelto**, ma non provato in questo item (vedi «Cosa non è provato»).
2. **kaniko** — Apache-2.0; il repo GoogleContainerTools/kaniko risulta *archiviato* (ultimo push
   2025-06-03, campo `archived: true` dell'API). Scartato.
3. **buildah** — Apache-2.0; in un pod serve user namespace e storage `vfs`/fuse: stessa famiglia di
   permessi di BuildKit rootless con meno integrazione con i `Dockerfile` esistenti. Riserva.
4. **Docker-in-Docker o socket montato** — richiede privilegi o accesso al runtime: escluso da CI2.

## Decisione

### 1. Esecutore

**act, modalità host, dentro un pod senza privilegi, un job per pod.** Licenza MIT, compatibile con
AGPL-3.0 (tabella sopra). L'immagine del job contiene act (versione e SHA256 fissati nel
Dockerfile), niente Docker. Il pod è il confine di isolamento; i passi sono processi nel pod.

Il binario è l'unità minima; passare ad act come libreria Go (`pkg/runner`) è un'ottimizzazione
successiva, non una decisione di questo ADR.

### 2. Scheduler e coda: dentro `core`

Nessun servizio nuovo (D-pochi servizi). `core` ha già Postgres, l'outbox e gli eventi:

- tabelle `ci_runs`, `ci_jobs`, `ci_steps` nello schema di `core` (migrazioni dopo il riallineamento a
  main, vedi quaderno: numerazione e `migrate.Down`);
- coda su Postgres con `SELECT … FOR UPDATE SKIP LOCKED`, coda equa per repo (CI7), concorrenza 1–3
  job (CI7), `concurrency:` dei workflow implementata dallo scheduler (act la ignora);
- il DAG (`needs`, `matrix`, `if` di job, `outputs`) lo risolve lo scheduler, perché un pod esegue un
  solo job (`act -j`): vedi rischio 1;
- un componente `ci-dispatcher` in `core` crea i pod con un ServiceAccount il cui Role vale **solo**
  nel namespace dei job (create/get/list/watch/delete pods, pods/log). `core` è l'unico servizio che
  riceve questo permesso; se in revisione si preferisce non darlo a `core`, l'alternativa è un
  secondo Deployment dello stesso binario con un solo ruolo: resta un'opzione, non un servizio nuovo.

### 3. Job = pod usa e getta, spazio separato

Provato su k3d ([`poc-ci-executor/`](poc-ci-executor/)):

- Namespace `gitstack-ci-jobs` con Pod Security `restricted` (enforce): un pod `privileged` viene
  rifiutato dall'admission (`logs/sicurezza-pod.txt`).
- Pod del job: `runAsNonRoot`, uid 10001, `seccompProfile: RuntimeDefault`,
  `allowPrivilegeEscalation: false`, `capabilities.drop: [ALL]`, `readOnlyRootFilesystem`,
  `automountServiceAccountToken: false`, `activeDeadlineSeconds: 3600`, risorse 2 vCPU / 4 GiB /
  20 GiB di disco effimero (lettura di CI7 «2 vCPU/4 GB/20 GB/h»: da confermare che «h» sia il
  limite di un'ora).
- **NetworkPolicy** `ci-job-egress`: ingress vuoto; egress solo verso `0.0.0.0/0` meno
  `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `169.254.0.0/16` (pod, service, nodi, LAN).
- **Cancello `gate` (init container, fail-closed)**: il CNI applica le NetworkPolicy in modo
  asincrono. Su k3d (kube-router) la prima prova, senza cancello, ha lasciato il job *raggiungere*
  API server, DNS e pod vicini, anche con la policy già creata (giro B1 in `logs/workflow-due-passi.txt`). Il cancello fa partire il job solo dopo che l'API server risulta irraggiungibile e
  fallisce il pod dopo 90 s. Senza di esso la barriera di CI2 non è garantita.
- **PriorityClass**: `gitstack-services` (1 000 000) > `gitstack-apps` (100 000) > `gitstack-ci-job`
  (1 000, `preemptionPolicy: Never`). I job non sfrattano nessuno e sono i primi a essere sfrattati.
  I valori sono una proposta; i servizi di GitStack (chart) e le app devono dichiarare la loro classe.
- **ResourceQuota** nel namespace dei job per far rispettare CI7 (3 pod, 6 CPU, 12 GiB di richieste):
  prevista, non provata.

**Un solo endpoint raggiungibile (`ci-gateway`).** Un job senza rete interna non può clonare il repo,
scaricare action dal mirror, inviare log e artefatti. Si decide che il pod possa raggiungere **un solo
endpoint** del cluster, una porta di `core` (o di un pod gemello) che parla il protocollo dei runner
(sezione 4), con token per job a scadenza breve; nessun altro pod, service, DNS del cluster o API
server. Prova: `networkpolicy-gateway.yaml` — il pod raggiunge il finto gateway, non API server, DNS
del cluster né un'altra porta dello stesso pod (`logs/endpoint-consentito.txt`).
**Questa è un'interpretazione di CI2 («no rete interna»): da confermare col board.** Se il board non
la accetta, il ripiego è nessuna rete verso il cluster: log letti da `core` con `pods/log`, workspace
iniettato dall'esterno, artefatti passati via stdout o volume; è più lento e limita le dimensioni.

DNS: il job non usa CoreDNS (la policy lo blocca). Il pod usa `dnsPolicy: None` con un resolver
configurabile dall'istanza; non provato.

### 4. Protocollo dei runner esterni (CI2, facoltativi)

Un solo protocollo per runner interni ed esterni: **pull** HTTPS, il runner apre sempre la
connessione (nessuna porta in ingresso sul runner), secondo lo schema `runner.v1` di Gitea
(`actions-proto-go`, MIT, ConnectRPC su HTTP): registrazione con token monouso → token del runner;
`FetchTask` a long-poll; `UpdateTask`/`UpdateLog` per stato e log. Con questo `act_runner` e il
runner Forgejo si possono collegare con configurazione, senza che GitStack ne distribuisca il codice.
Il pod interno usa lo stesso protocollo verso il `ci-gateway`. I runner esterni sono l'unica strada
per i job che il pod non può fare (Windows, VM con k3s, Docker): vedi elenco sotto.

**Non provato**: la compatibilità reale con `act_runner`/Forgejo runner va verificata nell'item che
implementa il protocollo; può servire un adattatore.

### 5. Log, artefatti e cache (CI5)

Disco, su un volume dedicato di `core` (`ci-data`), con due cartelle: `logs/` **nel backup** e `data/`
(artefatti e cache) **fuori dal backup**, come chiede CI5; metadati e indici in Postgres. Artefatti e
cache hanno un'interfaccia di storage con due implementazioni (GIT-280): disco e S3 (N7, richieste
firmate SigV4); i log restano su disco. Log 90 giorni, artefatti 30 giorni fino a 2 GB, cache 10 GB per
repo (le meno usate vanno via a quota piena, quelle non usate da 7 giorni comunque): tutti i valori li
regola l'amministratore (`GET`/`PUT /admin/ci/storage`, tabella `core.ci_settings`) e un giro orario di
pulizia li applica. Il ruolo del `ci-gateway` è l'unica scrittura: il job non vede il volume.

**Compatibilità con GitHub (GIT-280).** Le cache parlano il protocollo v1 di `actions/cache`
(`ACTIONS_CACHE_URL = <ci-gateway>/ci/v1/cache/`: ricerca, prenotazione, pezzi con `Content-Range`,
conferma). `actions/cache` v4 e `actions/upload-artifact` v4 usano invece un protocollo Twirp con
upload su Azure Blob: **non è compatibile** e core non lo implementa. Gli artefatti si caricano con
`PUT /ci/v1/artifacts/{nome}` del ci-gateway (corpo = ZIP), con il token del job. Nel backup i dati
delle tabelle `ci_artifacts` e `ci_caches` sono esclusi (`pg_dump --exclude-table-data`), così dopo un
restore non restano righe che puntano a file mancanti.

### 6. Build di immagini (CI2)

Job di tipo `build` in un namespace separato `gitstack-ci-build`, BuildKit rootless senza demone,
nessun socket. Attenzione: BuildKit rootless di norma chiede `seccompProfile` e AppArmor
*Unconfined*, che Pod Security `restricted` e `baseline` rifiutano. Il namespace dovrà quindi avere
una policy dedicata (ValidatingAdmissionPolicy che accetta *solo* quella forma di pod) o un livello
di Pod Security più alto, con la stessa NetworkPolicy e lo stesso cancello. **Non provato qui**:
è il primo rischio della v2-a-build e va verificato su k3d/VM prima di promettere CI2 sulle build.

## Quanto girano i workflow di GitStack (`.github/workflows`)

Provato: `act -l` (mappa di `logs/act-list-workflow-gitstack.txt`) legge tutti e 6 i workflow e
trova 28 job: la sintassi non è un ostacolo. **Non è stato eseguito nessuno dei workflow reali**:
quello che segue viene dalla lettura dei file e da cosa sa fare l'esecutore in modalità host.

Funzioni che **non** sono supportate dall'esecutore scelto (o dal pod) e vanno riscritte o spostate:

1. `services:` (job `identity-oidc`, Postgres) e `docker run` (Keycloak): servono Docker.
2. `docker build`, `docker buildx imagetools …`, `docker/build-push-action`,
   `docker/login-action`, `docker/metadata-action` (job `registry`, `images`, `chart`, `security`
   con Trivy sulle immagini, `install.yml`): passano al tipo `build` (sezione 6); `imagetools
   inspect/create` non ha equivalente.
3. Job `chart` (k3d) e `install-ubuntu-22` / `e2e` (`sudo ./deploy/install.sh`, k3s sul runner):
   vogliono un host o una VM veri: **runner esterno**, non il pod.
4. `runs-on: windows-latest` (job `pwsh`): runner esterno Windows. `runs-on: ubuntu-22.04` e la
   matrice `os` di `install.yml`: nel pod esiste una sola immagine; le etichette vanno mappate.
5. `id-token: write` e firma keyless di cosign con identità `https://github.com/<repo>/…`
   (job `sign`, `release`): senza OIDC di GitHub non c'è token; per RG2/v3 la firma passa a una chiave
   dell'istanza e le verifiche dei workflow vanno riscritte.
6. `gh` CLI e `GH_TOKEN: secrets.GITHUB_TOKEN` (`gh release create/upload/view/download`, `gh pr create`):
   parlano con l'API di GitHub; vanno sostituiti da REL1 e dalla CLI `gs`.
7. `actions/deploy-pages`, `actions/upload-pages-artifact`, `environment:` (job `docs`):
   specifici di GitHub Pages.
8. `needs.<job>.outputs.*` e artefatti fra job (un pod = un job): lo scheduler dovrà iniettarli
   (rischio 1); `install.yml` ha un `outputs:`.
9. `concurrency:` e `permissions:`: act le legge e le ignora; devono essere realizzate dallo scheduler
   e dal token del job (CI4).
10. Strumenti preinstallati sul runner GitHub (`go`, `helm`, `jq`, `shellcheck`, `docker`, `gh`,
    `sudo`): l'immagine del job di prova ha solo node e git. Serve un'immagine con la toolchain
    o più download in esecuzione; `sudo` non esiste (non root, `NoNewPrivs`).
11. Cache delle action (`cache: pnpm` di `setup-node`, `cache-dependency-path` di `setup-go`) e
    `upload-artifact@v6`: richiedono i servizi di cache e artefatti del `ci-gateway`; non provati
    con le versioni in uso (v6/v5) — act ha un server proprio, ma la sua copertura delle versioni
    recenti non è verificata.
12. `actions/checkout` e le altre action: scaricano da GitHub; in GitStack vengono dal mirror
    interno (CI1) e il checkout del repo passa dal `ci-gateway`, non dalla rete interna.

Per il resto — `run`, `env`, `if`, `matrix`, `workflow_dispatch`, `schedule`, `workflow_call` locale
(`uses: ./.github/workflows/security.yml`), `GITHUB_OUTPUT`/`GITHUB_STEP_SUMMARY`, action composite
locali — l'esecutore li gestisce; il workflow di prova usa `run`, `GITHUB_OUTPUT`, `with:` e un'action
composite locale. Le action JavaScript (`setup-go`, `setup-node`, `checkout`) richiedono node
nell'immagine e il mirror, e non sono state eseguite.

## Prova di fattibilità: esito

`poc-ci-executor/job-pod.yaml` + `k8s.yaml`, su k3d v5.9.0 (k3s v1.35.5), act v0.2.89:

- giro A (controllo, senza NetworkPolicy): il passo 2 trova API server, DNS e pod vicino
  *raggiungibili* e fallisce → la sonda funziona;
- giro B1 (policy senza cancello): il job parte prima delle regole e vede il cluster, fallisce;
- giro B2 (policy e cancello): `Job succeeded`; passo 1 `uid=10001`, `CapEff` vuoto,
  `NoNewPrivs: 1`; passo 2 (action composite locale) riceve l'output del passo 1; API server,
  DNS del cluster e pod vicino «bloccato»; Internet raggiungibile come da policy.
  Log: `poc-ci-executor/logs/workflow-due-passi.txt`.

Vincoli emersi:
- k3d 5.9 / k3s 1.35 su host con cgroup v1 (Docker Desktop qui) non parte: `--k3s-arg
  "--kubelet-arg=fail-cgroupv1=false@server:0"`; senza, il kubelet esce.
- act in modalità host parte con workspace vuoto: le action locali (`uses: ./…`) si trovano solo dopo
  un passo che lo popola (l'equivalente di `actions/checkout`). `--bind` non cambia nulla in modalità
  host. Il workflow di prova ha quindi un passo «Checkout (copia locale)» davanti ai due passi.
- `git` rifiuta il workspace per «dubious ownership» se il tar conserva il proprietario:
  `tar --no-same-owner` e `safe.directory`.

## Conseguenze

- Nessun servizio nuovo; `core` cresce (scheduler, coda, storage CI, protocollo runner) e riceve un
  permesso Kubernetes limitato a un namespace. Tenere il codice in un pacchetto separato (`ci/`).
- Una sola immagine di job da costruire, firmare e aggiornare (act, node, toolchain). Il chart deve
  dichiarare namespace, PSA, PriorityClass, NetworkPolicy e ResourceQuota; l'installer su k3s deve
  verificare che il CNI applichi le NetworkPolicy.
- Un pod per job costa più di un processo: ogni job paga l'avvio del pod e la copia del workspace.
- I workflow di rilascio di GitStack non girano sulla CI di GitStack senza riscrittura (punti 1–7).
  Finché non esistono, il ponte V7 (GitHub Actions su main e tag) resta [c_53094f123bd44a34].

## Rischi e cose da verificare

1. **`-j` e `needs`**: act con un solo job non conosce gli `outputs` dei job precedenti; lo scheduler
   deve iniettarli (variabili/evento) o i job dipendenti vanno in uno stesso pod. Da provare.
2. **Limiti di act**: sviluppo e copertura delle action recenti non sono garantiti. Mitigazione:
   versione e SHA256 fissati, suite di workflow di regressione nel repo, uscita verso un esecutore
   nostro solo se i punti 8, 11 non reggono.
3. **Barriera di rete**: dipende dal CNI. Il cancello `gate` è obbligatorio; il chart deve fallire
   se la NetworkPolicy non è applicata. Su cluster con CNI senza policy la CI resta spenta.
4. **Build rootless** (sezione 6) e **compatibilità dei runner esterni** (sezione 4): non provate.
5. Protocollo `runner.v1`: dipendenza da un protocollo di terzi; si copia lo schema, si versiona il nostro.

## Cosa non è provato in questo item

BuildKit rootless; ResourceQuota; coda e concorrenza; DNS del job; protocollo dei runner esterni;
esecuzione di un workflow reale di GitStack; action JavaScript e mirror; `needs`/outputs fra pod;
più nodi (la prova è su un nodo solo).
