# Lista di controllo della release candidate (V1)

Regola V1 di `.prisma/knowledge/topics/rilascio-v1.md` (M-09/B, GIT-226): i criteri di uscita sono tutti obbligatori per la v1.0, e prima della v1.0 escono una o più `v1.0.0-rc.N`. Questa lista ha un passo per criterio e dice come verificarlo. Il tag `v1.0.0-rc.1` lo crea il board, non un item.

## Come si usa

Dalla radice del repo:

```sh
go run scripts/release-readiness.go            # stampa automatici e manuali, uscita 0
go run scripts/release-readiness.go --strict   # uscita 1 se un criterio automatico è aperto
go run scripts/release-readiness.go --ci       # aggiunge l'ultimo giro di smoke e install su main (serve gh)
make release-readiness                         # equivale al primo
```

- Un criterio automatico può essere **soddisfatto**, **non ancora soddisfatto** (con l'elenco di cosa manca) o **non verificabile qui** (strumento assente: passa ai manuali). Un criterio aperto non è un errore dello script: l'uscita è 0, salvo `--strict` (1). L'uscita 2 è un errore vero dello script (file della tabella illeggibile o registro non trovato).
- «Script verde» vuol dire `--strict` con uscita 0 sull'ultimo commit di main, più i passi manuali qui sotto spuntati dal board.
- Lo script legge solo file del repo (e `gh` con `--ci`): non tocca l'istanza e non crea tag.

## Passi

| # | Criterio V1 | Come si verifica | Tipo |
|---|---|---|---|
| 1 | M-01…M-08 completate | Le milestone sono chiuse sul board. | manuale (M1) |
| 2 | Ogni regola decisa è coperta da almeno un test automatico | `go run scripts/check-rules-coverage.go` passa e `docs/rules-coverage.md` non ha righe `da compilare` né `non coperta`. Le righe `parziale` dichiarano cosa manca e si leggono a mano. | automatico (A1) |
| 3 | Nessun bug critico o alto aperto | Nel registro di sicurezza nessuna riga F<n> `high` o `critical` aperta (A3); nel tracker nessuna issue con priorità critica o alta aperta (le medie vanno nelle note di rilascio). | automatico (A3) e manuale (M2) |
| 4 | Revisione di sicurezza chiusa | Ogni riga F<n> del registro in fondo a `docs/security/asvs-l2.md` è «Resolved» o `ok` (A2); la riga V2 di `rules-coverage.md` passa a `coperta`; `SECURITY.md` passa il controllo (A8). | automatico (A2, A8) |
| 5 | Test di fumo superati | `.github/workflows/smoke.yml` e `TestCaricoFumo` presenti (A4); l'ultimo giro di `smoke` su main è verde (A9 con `--ci`, oppure la scheda Actions). | automatico (A4, A9) e manuale (M3) |
| 6 | Installazione, aggiornamento dalla release candidate e ripristino verdi in CI | `.github/workflows/install.yml` copre Ubuntu 22.04 e 24.04 e il passo «Dopo il restore» (A5); l'ultimo giro su main è verde (A10 con `--ci`, oppure Actions). Pop!_OS non ha un runner: si prova a mano sulla VM (`deploy/test-vm`). | automatico (A5, A10) e manuale (M3) |
| 7 | Rilascio firmato su tag provato | Il job `release` di `ci.yml` e `scripts/release-changelog.sh` ci sono (A7); il job non è provato su un tag reale: un tag di prova `v0.0.0-test.N` su un fork, prima di `v1.0.0-rc.1` (riga V4 di `rules-coverage.md`). | automatico (A7) e manuale (M4) |
| 8 | Un agente chiude una issue end-to-end solo con `gs` e skills | `TestAgentTrial` e `scripts/agent-trial/check.sh` presenti (A6); la prova reale si fa con `docs/agent-trial.md` su una VM o su homehub e `check.sh` stampa solo `PASS`. | automatico (A6) e manuale (M5) |
| 9 | Dogfooding di almeno 4 settimane senza perdite di dati | Il conto parte dalla tappa 2 di V7 (passaggio a GitStack, `docs/operations.md` e `AGENTS.md`). Il board registra la data di inizio e controlla che nessun backup, restore o mirror abbia perso dati. Non è un test automatico. | manuale (M6) |
| 10 | Tag della release candidate | Il board crea `v1.0.0-rc.1` solo con i passi 1–9 spuntati; la v1.0.0 segue dopo le rc. | manuale (M7) |

## Registro delle prove (da compilare dal board per ogni rc)

| rc | Commit di main | `--strict` | M1 | M2 | M3 | M4 | M5 | M6 | Data |
|---|---|---|---|---|---|---|---|---|---|
| `v1.0.0-rc.1` | | | | | | | | | |
