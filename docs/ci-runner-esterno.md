# Runner esterno della CI

GIT-278 (CI2, ADR 0001 §4). Un runner esterno è un processo su una macchina tua (Linux, macOS, Windows, una VM)
che prende i job che il pod interno non può fare: quelli con `runs-on:` diverso da `ubuntu-*`
(`self-hosted`, `windows-…`, `macos-…`, `gpu`, un'etichetta tua). Usa lo stesso protocollo `runner.v1` dei pod interni:
è **pull**, il runner apre sempre la connessione e non serve nessuna porta in ingresso sulla macchina.

## Cosa serve

- Il binario `gitstack-runner` (sotto) e [`act`](https://github.com/nektos/act) nel `PATH` (o `--act <percorso>`):
  il runner esegue il job con `act` in modalità host, come il pod interno. Sulla macchina girano i comandi dei workflow:
  usa una macchina (o un utente) dedicata alla CI.
- Raggiungere il **ci-gateway esterno** dell'istanza (porta dedicata di core, non quella dell'API): lo espone
  l'amministratore con l'opzione `ci.externalGateway` del chart, **spenta per default** (sezione sotto).

## 0. Esporre il ci-gateway (amministratore, GIT-329)

Il ci-gateway interno (`ci.gateway.port`, 8081) è raggiungibile solo dai pod dei job. Per i runner esterni il chart ha
un'opzione spenta per default, `ci.externalGateway` (dettagli in [`deploy/gitstack/README.md`](../deploy/gitstack/README.md)):

```sh
helm upgrade gitstack deploy/gitstack --reuse-values \
  --set ci.externalGateway.enabled=true --set ci.externalGateway.host=ci.gitstack.lan
```

Core apre una seconda porta (`GITSTACK_CORE_CI_EXTERNAL_ADDR`, 8082) e Traefik la pubblica con una `IngressRoute` sull'host
dedicato, solo HTTPS, con il certificato dell'istanza (la CA interna; deve coprire quell'host). Richiede
`ingress.tls.enabled`. La porta esterna accetta **solo token di runner registrati** (`gsr_…`): il biglietto di un pod
interno (`gsq_…`) riceve `Unauthenticated` e la NetworkPolicy dei job non la raggiunge. Un host dedicato, non un path, perché
il protocollo vive su percorsi suoi (`/runner.v1.RunnerService/…`, `/api/actions/…`, `/ci/v1/archive`) e il resto dell'host
risponde 404.

## 1. Scarica il runner

I binari sono sull'istanza, senza internet: `https://<host>/downloads/gitstack-runner_<os>_<arch>[.exe]`
(`linux`, `darwin`, `windows` × `amd64`, `arm64`). Il checksum è in `SHA256SUMS` e l'elenco in `index.json` (campo `runners`).

```sh
base=https://<host>/downloads
curl -fsSLO "$base/gitstack-runner_linux_amd64" -O "$base/SHA256SUMS"
grep ' gitstack-runner_linux_amd64$' SHA256SUMS | sha256sum -c -
chmod +x gitstack-runner_linux_amd64 && mv gitstack-runner_linux_amd64 /usr/local/bin/gitstack-runner
```

(Con la CA interna, vedi `deploy/gitstack/README.md`, «Download di gs e skills».) Gli stessi file sono negli asset della
release `sha-<commit>` / `vX.Y.Z`.

## 2. Registra il runner

Solo l'amministratore dell'installazione crea il token di registrazione: **monouso**, vale un'ora, si vede **una volta sola**
nella risposta alla creazione (nel database c'è solo l'hash e nessuna GET lo restituisce).

```sh
curl -fsS -X POST https://<host>/api/v1/admin/ci/runners/registration-token -H "Authorization: Bearer <token-admin>"
# {"token":"gsrt_…","expiresAt":"…"}
```

Sulla macchina del runner:

```sh
gitstack-runner register --url https://<ci-gateway> --token gsrt_… --name build-box --labels linux,gpu
```

`self-hosted` c'è sempre; le etichette sono in minuscolo. Il nome è unico (un nome già usato non consuma il token).
La registrazione scrive `config.json` (permessi `0600`) in `~/.config/gitstack-runner/` (`%AppData%\gitstack-runner\` su Windows)
con il **token del runner** (`gsr_…`): chi lo legge può prendere job al posto del runner, perciò il file e la cartella di lavoro
non vanno resi leggibili a chi scrive i workflow.

## 3. Avvia

```sh
gitstack-runner run            # --workdir, --act, --config, --action-mirror
```

Il runner esegue un job per volta, in ciclo, finché non lo fermi. Come servizio, esempio systemd:

```ini
[Service]
User=gitstack-runner
ExecStart=/usr/local/bin/gitstack-runner run
Restart=always
```

## Come si scelgono i job

Un job con `runs-on:` che contiene anche una sola etichetta **diversa da `ubuntu-*`** va ai runner esterni
(`runs-on: [self-hosted, linux]`, `runs-on: gpu`, `runs-on: linux`): così decide `runnerTypeOf` (GIT-278, confermato da GIT-329).
Lo prende un runner le cui etichette le **contengono tutte** (`self-hosted` è implicita). Un job senza nessun runner
compatibile resta in coda.

**Attenzione a `runs-on: linux`.** `linux` non è `ubuntu-*`: il job non va a un pod interno, va ai runner esterni, e se
nessun runner ha registrato l'etichetta `linux` resta `queued` senza errori. Per usare il runner integrato scrivi
`runs-on: ubuntu-latest` (o un altro `ubuntu-*`); per un tuo runner registralo con quell'etichetta
(`--labels linux`). La UI mostra solo che il job è in coda su un «external runner» (`runnerType: external` nel contratto):
non c'è ancora un campo col motivo dell'attesa, perciò il motivo è questo. I job `ubuntu-*` restano ai pod interni e non
contano nella capacità del cluster (CI7); un runner esterno esegue un solo job alla volta.

## Compatibilità con il runner di Gitea (act_runner)

Il ci-gateway parla `runner.v1` di `actions-proto` come il runner di Gitea/Forgejo (oggi `gitea.com/gitea/runner`, già
`act_runner`). Il test `TestActRunner_Compat` (`services/core/internal/cigateway/actrunner_integration_test.go`, passo CI
«Compatibilità con il runner di Gitea») registra il binario **pinnato** (v5.2.2, sha256
`5a2639fd2a57feeba29a9b1fb0e941489f092a4fa681c7c333c0372731248b63` per `linux-amd64`) col token monouso, esegue un job
`runs-on: self-hosted` e verifica log ed esito `success`:

```sh
gitea-runner register --no-interactive --instance https://<ci-gateway> --token gsrt_… --name box --labels self-hosted:host
gitea-runner daemon
```

Per arrivarci il gateway ha dovuto fare tre cose in più rispetto al nostro `gitstack-runner` (le incompatibilità trovate dalla
prova, con il messaggio d'errore originale):

1. **Prefisso `/api/actions`**: il runner di Gitea chiama `<istanza>/api/actions/<servizio>/<metodo>`. Prima il gateway
   rispondeva 404 e la registrazione falliva con `cannot ping the Gitea instance server: unimplemented: 404 Not Found`.
   Ora toglie il prefisso.
2. **`PingService`**: il runner lo chiama prima di registrarsi (stesso errore sopra). Ora c'è, senza credenziali, come in Gitea.
3. **Token del runner su `UpdateLog`/`UpdateTask`**: il runner di Gitea riusa il proprio token (`gsr_…`) su ogni chiamata e
   non usa il token del job; il log arrivava con `unauthenticated: token del job non valido` (il job restava `in_progress`).
   Ora `gsr_…` vale anche per queste due chiamate, ma solo per il job che quel runner sta eseguendo.

Limiti noti: il runner di Gitea esegue il job con il suo `act`, non con quello di `gitstack-runner`, quindi `uses:`,
mirror delle action, cache e artefatti dipendono dalla sua configurazione e non sono coperti dalla prova (che usa passi
`run:`). Con il runner di Gitea il token del job (`GITHUB_TOKEN`) è mascherato nei log solo se il gateway non si è riavviato
a metà job (il masker è in memoria). Provato su Linux; Windows e macOS non sono coperti dalla prova.

## Stato e revoca

`GET /api/v1/admin/ci/runners` (solo amministratore) elenca i runner con etichette e stato: `online` (si è fatto vivo da meno
di 90 s), `busy` (ha un job in corso), `offline`. `DELETE /api/v1/admin/ci/runners/{runnerId}` revoca il runner: il suo
token non vale più, non prende altri job e il job che stava eseguendo è chiuso come `cancelled`. Per registrarlo di nuovo serve un
nuovo token di registrazione.

## Come per il runner integrato

Il runner esterno riceve il token del job (`GITHUB_TOKEN`, `gsj_…`, stessi permessi e stessa scadenza) e i segreti del repo,
e pubblica log e stati dei passi con `UpdateLog` / `UpdateTask`. Il gateway maschera i segreti e il token del job nelle righe di log
prima di scriverle, identico al pod interno. Se il runner sparisce senza chiudere il job, il job resta `in_progress` finché
l'amministratore revoca il runner (non c'è ancora un timeout automatico).
