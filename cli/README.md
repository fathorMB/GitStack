# cli/

`gs`, la CLI Go di GitStack per persone e agenti (stile `gh`), su client generato dall'OpenAPI del gateway. Dettagli: [[M-07]] in `.lmbrain-lite/milestones/M-07.md`.

Stato: scheletro di modulo Go (vedi `go.mod`, incluso in `go.work` in radice). I comandi (`auth`, `repo`, `issue`, `api`, ...) arrivano con M-07.

## Licenza

Apache-2.0, non AGPL-3.0 (vedi `LICENSE` in questa cartella). Decisione D17 [c_1d1d61aca3dea601]: il client (CLI e skills) usa una licenza permissiva per non porre barriere all'integrazione negli strumenti degli agenti di coding; il cuore del prodotto (i servizi in `services/`) resta protetto da AGPL-3.0.

## Distribuzione (GIT-171, G6)

I binari si costruiscono con `scripts/build-gs-dist.sh <cartella> <versione>`: `gs_<os>_<arch>` per linux, darwin e windows su amd64 e arm64 (`.exe` su windows), `SHA256SUMS`, `gs-skills.zip` (cartella `skills/`) e `index.json`. La versione (`-X main.version=`) è `sha-<commit>`, come il tag delle immagini. Lo stesso script gira nel job CI `gs-binaries` (asset della release `sha-<commit>`, solo dopo il merge) e nello stage `gs` di `web/Dockerfile`: ogni istanza serve i file su `/downloads` e gli script `/install-gs.sh` e `/install-gs.ps1` (sorgenti in `web/deploy/`), senza internet. Dettagli, rotte e gestione della CA interna: `deploy/gitstack/README.md`, sezione «Download di gs e skills».

```sh
curl -fsSL https://<host>/install-gs.sh | sh        # Linux, macOS
irm https://<host>/install-gs.ps1 | iex             # Windows
```
