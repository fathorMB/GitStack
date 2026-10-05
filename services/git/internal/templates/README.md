# templates

Modelli di contenuto iniziale di un repository (R5): `.gitignore`, licenza e
README. I testi sono incorporati con `embed`; nessuna dipendenza esterna.
La provenienza dei testi è nel file [NOTICE](NOTICE).

## API

- `GitignoreIDs()`, `LicenseIDs()`: id ordinati.
- `Gitignore(id)`: testo del modello.
- `License(id, year, holder)`: testo della licenza; `{{year}}` e `{{holder}}`
  sono sostituiti in MIT, BSD-2-Clause e BSD-3-Clause, le altre tornano uguali.
- `Readme(name, description)`: `# <name>`, poi la descrizione se non è vuota.
- `ErrUnknownTemplate`: per un id sconosciuto (usare `errors.Is`).

## Id stabili

Sono gli stessi dell'enum del contratto API (GIT-63). Non si cambiano né si
aggiungono senza cambiare anche il contratto.

| `.gitignore` | origine (github/gitignore) |
|---|---|
| `go` | Go |
| `node` | Node |
| `python` | Python |
| `java` | Java |
| `dotnet` | VisualStudio |
| `rust` | Rust |
| `cpp` | C++ |
| `terraform` | Terraform |
| `ruby` | Ruby |
| `php` | Composer |

| Licenza | SPDX |
|---|---|
| `mit` | MIT |
| `apache-2.0` | Apache-2.0 |
| `gpl-3.0` | GPL-3.0-only |
| `agpl-3.0` | AGPL-3.0-only |
| `lgpl-3.0` | LGPL-3.0-only |
| `mpl-2.0` | MPL-2.0 |
| `bsd-2-clause` | BSD-2-Clause |
| `bsd-3-clause` | BSD-3-Clause |
| `unlicense` | Unlicense |

Nota: la LGPL-3.0 è un'aggiunta alla GPL-3.0 e va affiancata ad essa.
