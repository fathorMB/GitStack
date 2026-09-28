# cli/

`gs`, la CLI Go di GitStack per persone e agenti (stile `gh`), su client generato dall'OpenAPI del gateway. Dettagli: [[M-07]] in `.lmbrain-lite/milestones/M-07.md`.

Stato: scheletro di modulo Go (vedi `go.mod`, incluso in `go.work` in radice). I comandi (`auth`, `repo`, `issue`, `api`, ...) arrivano con M-07.

## Licenza

Apache-2.0, non AGPL-3.0 (vedi `LICENSE` in questa cartella). Decisione D17 [c_1d1d61aca3dea601]: il client (CLI e skills) usa una licenza permissiva per non porre barriere all'integrazione negli strumenti degli agenti di coding; il cuore del prodotto (i servizi in `services/`) resta protetto da AGPL-3.0.
