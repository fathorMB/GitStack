# Modelli di issue (M-05/H, GIT-108)

I modelli di issue sono file Markdown in `.gitstack/ISSUE_TEMPLATE/` nel
branch principale del repo. La UI, l'API `listIssueTemplates` (`GET
/repos/{owner}/{repo}/issue-templates`) e la CLI `gs` li leggono per
proporre titoli, etichette e testo precompilato quando si crea una issue.

## Formato

Ogni file `.md` nella cartella è un modello. Il nome del file (senza
estensione `.md`) diventa il `name` dell'API.

Il contenuto può avere un'intestazione YAML (front matter) racchiusa tra
due righe `---`. Le chiavi del front matter sono facoltative:

| Chiave | Tipo | Descrizione |
|---|---|---|
| `title` | stringa | Titolo proposto per la nuova issue |
| `about` | stringa | Breve descrizione del modello (usato come `about` nella risposta API) |
| `labels` | lista di stringhe | Etichette da aggiungere automaticamente alla issue |

Il testo dopo il secondo `---` è il corpo Markdown (`body`) del modello.

### Esempio

```yaml
---
title: Bug Report
about: Segnala un problema nel software
labels:
  - bug
  - triage
---

## Steps to reproduce

Descrivi qui i passaggi per riprodurre il problema.

## Comportamento atteso

Cosa ti aspettavi che succedesse?

## Comportamento effettivo

Cosa succede invece?
```

### File senza front matter

Un file `.md` senza front matter è un modello valido: il `name` viene dal
nome del file, il `body` è l'intero contenuto del file, e `title`,
`about`, `labels` sono null.

```markdown
## Descrizione

Inserisci qui la descrizione della tua issue.
```

## Comportamento

- Solo i file con estensione `.md` sono considerati modelli.
- Cartelle, file non `.md` e sottocartelle sono ignorati.
- Un modello con front matter YAML non valido viene saltato con un avviso
  nel log del servizio core; gli altri modelli restano nell'elenco.
- La risposta API `{items}` è ordinata alfabeticamente per `name`.
- Se la cartella `.gitstack/ISSUE_TEMPLATE/` non esiste o il repo è vuoto,
  la risposta è `{items: []}`.

## Contratto API

`GET /repos/{owner}/{repo}/issue-templates` richiede `read:resource` e il
ruolo `read` sul repo. Un repo non leggibile risponde 404.

Schema `IssueTemplate` (api/openapi.yaml):

| Campo | Tipo | Obbligatorio | Descrizione |
|---|---|---|---|
| `name` | string | sì | Nome del file senza estensione |
| `title` | string | no | Titolo dal front matter |
| `about` | string | no | Descrizione dal front matter |
| `labels` | string[] | no | Etichette dal front matter |
| `body` | string | sì | Testo Markdown senza front matter |
