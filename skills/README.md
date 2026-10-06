# skills/

Pacchetto di skills per agenti di coding nel formato aperto Agent Skills: una cartella per skill con un `SKILL.md` (frontmatter `name` e `description`, poi le istruzioni). Descrivono i flussi tipici su GitStack e si usano insieme alla CLI `gs`. Dettagli: [[M-07]] in `.lmbrain-lite/milestones/M-07.md`.

Stato: v1 con 5 skills (M-07/I, GIT-170).

| Skill | Quando serve |
| --- | --- |
| `gitstack-setup` | installare `gs`, login, scelta dell'istanza |
| `gitstack-issue-to-commit` | lavorare su una issue fino al commit `fixes #n` (C1, C2) |
| `gitstack-issue-triage` | cercare, classificare, assegnare e chiudere le issue |
| `gitstack-repo-admin` | creare, modificare, archiviare, eliminare e recuperare repo |
| `gitstack-notifications` | ciclo notifiche → lavoro → risposta (C4) |

Ogni skill vieta agli agenti `--yes` su eliminazioni e archiviazioni senza un'istruzione esplicita di una persona (G8). I comandi `gs` citati nei blocchi di codice li verifica un test (`cli/internal/cmd/skills`): devono esistere, con i loro flag.

## Installare

Le skills sono servite dall'istanza (`/downloads/gs-skills.zip`, con lo sha256 in `/downloads/index.json`) e le installa `gs`:

```
gs skills install                 # agenti riconosciuti, nel progetto (radice del repo git)
gs skills install --user          # a livello utente (home)
gs skills install --agent codex   # un agente a scelta (claude, codex)
gs skills install --agents-md     # in più, la sezione delimitata in AGENTS.md
gs skills update                  # allinea alla versione dell'istanza
```

Lo zip si verifica prima di scrivere: con lo sha256 sbagliato non si scrive niente. In ogni cartella installata c'è `.gs-skills-version` (sha256 e versione), che `update` confronta con l'istanza. Una cartella esistente non installata da `gs` non si tocca senza `--force`.

### Destinazioni

| Agente | Progetto | Utente | Riconosciuto da |
| --- | --- | --- | --- |
| Claude Code | `.claude/skills/<nome>/` | `~/.claude/skills/<nome>/` | `.claude/` nel progetto o in home |
| Codex | `.agents/skills/<nome>/` | `~/.agents/skills/<nome>/` | `.codex/` o `.agents/` nel progetto o in home |

Fonti ufficiali (verificate il 2026-10-06): Claude Code, <https://code.claude.com/docs/en/skills> (personali `~/.claude/skills/<skill-name>/SKILL.md`, di progetto `.claude/skills/<skill-name>/SKILL.md`); Codex, <https://developers.openai.com/codex/skills> (`$REPO_ROOT/.agents/skills`, `$HOME/.agents/skills`).

### AGENTS.md

`--agents-md` scrive in `AGENTS.md` (radice del progetto) una sezione breve tra `<!-- gitstack-skills:start -->` e `<!-- gitstack-skills:end -->`. Rieseguito la sostituisce, non la duplica; il resto del file resta identico byte per byte, e il fine riga (LF o CRLF) è quello del file.

## Licenza

Apache-2.0, non AGPL-3.0 (vedi `LICENSE` in questa cartella). Stessa motivazione di `cli/`: decisione D17 [c_1d1d61aca3dea601], nessuna barriera all'uso da parte di agenti e strumenti di terzi.
