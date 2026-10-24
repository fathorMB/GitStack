# Runner esterno della CI

GIT-278 (CI2, ADR 0001 §4). Un runner esterno è un processo su una macchina tua (Linux, macOS, Windows, una VM)
che prende i job che il pod interno non può fare: quelli con `runs-on:` diverso da `ubuntu-*`
(`self-hosted`, `windows-…`, `macos-…`, `gpu`, un'etichetta tua). Usa lo stesso protocollo `runner.v1` dei pod interni:
è **pull**, il runner apre sempre la connessione e non serve nessuna porta in ingresso sulla macchina.

## Cosa serve

- Il binario `gitstack-runner` (sotto) e [`act`](https://github.com/nektos/act) nel `PATH` (o `--act <percorso>`):
  il runner esegue il job con `act` in modalità host, come il pod interno. Sulla macchina girano i comandi dei workflow:
  usa una macchina (o un utente) dedicata alla CI.
- Raggiungere il **ci-gateway** dell'istanza (porta dedicata, `GITSTACK_CORE_CI_GATEWAY_ADDR`, non quella dell'API).
  L'esposizione del ci-gateway fuori dal cluster (Ingress o NodePort con TLS) è una scelta dell'amministratore e
  **non è configurata dal chart**: senza, il runner esterno non lo raggiunge.

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

Un job con `runs-on:` che contiene anche una sola etichetta non `ubuntu-*` va ai runner esterni
(`runs-on: [self-hosted, linux]`, `runs-on: gpu`). Lo prende un runner le cui etichette le **contengono tutte**
(`self-hosted` è implicita). Un job senza nessun runner compatibile resta in coda. I job `ubuntu-*` restano ai pod interni e non
contano nella capacità del cluster (CI7); un runner esterno esegue un solo job alla volta.

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
