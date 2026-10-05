# languages

Calcola la ripartizione per lingua di un ref (pannello About, mockup 07) dall'elenco dei file di `git ls-tree -r -l`. Lo usa `gitread.Service.Languages`, che serve `GET /internal/git/repos/{repoId}/languages` (`gitGetLanguages`); il risultato è in cache per sha del commit (`Cache`, al massimo 256 commit).

Il contratto non porta il colore: la UI lo prende da questa tabella (`Table` in `table.go`, `Color(name)`).

## Regole

- Conta solo chi ha una lingua in tabella, dal nome esatto del file (`Makefile`, `Dockerfile`...) o dall'estensione, senza distinguere le maiuscole. I binari (immagini, archivi, `.dat`) e i formati di dati o prosa (JSON, YAML, Markdown, testo) non hanno lingua e restano fuori: l'ls-tree non dà il contenuto, quindi i binari si escludono così.
- Esclusi: symlink, sottomoduli, file vuoti, cartelle `vendor` e `node_modules` a qualsiasi profondità, file generati dal nome (`*.gen.go`, `*.pb.go`, `*.pb.gw.go`, `*_generated.go`, `*.min.js`, `*.min.css`, `*.bundle.js`, `*.d.ts`).
- Ordine per byte decrescenti, a parità per nome. Repo vuoto: lista vuota e `totalBytes` 0.

## Arrotondamento

`percent` ha un decimale. Si parte dalla parte intera di `bytes * 1000 / totale` (decimi di punto) e i decimi che mancano a 1000 si danno, uno a testa, alle lingue col resto più alto (a parità, quella con più byte): la somma è sempre 100,0.

## Tabella delle lingue

| Lingua | Colore | Estensioni | Nomi di file |
|---|---|---|---|
| Go | `#00ADD8` | `.go` |  |
| TypeScript | `#3178C6` | `.ts` `.tsx` `.mts` `.cts` |  |
| JavaScript | `#F1E05A` | `.js` `.jsx` `.mjs` `.cjs` |  |
| Python | `#3572A5` | `.py` `.pyi` |  |
| Java | `#B07219` | `.java` |  |
| Kotlin | `#A97BFF` | `.kt` `.kts` |  |
| C | `#555555` | `.c` `.h` |  |
| C++ | `#F34B7D` | `.cc` `.cpp` `.cxx` `.hpp` `.hh` `.hxx` |  |
| C# | `#178600` | `.cs` |  |
| Rust | `#DEA584` | `.rs` |  |
| Ruby | `#701516` | `.rb` `.rake` | `Rakefile` `Gemfile` |
| PHP | `#4F5D95` | `.php` |  |
| Swift | `#F05138` | `.swift` |  |
| Dart | `#00B4AB` | `.dart` |  |
| Scala | `#C22D40` | `.scala` `.sc` |  |
| Elixir | `#6E4A7E` | `.ex` `.exs` |  |
| Haskell | `#5E5086` | `.hs` |  |
| Lua | `#000080` | `.lua` |  |
| Perl | `#0298C3` | `.pl` `.pm` |  |
| R | `#198CE7` | `.r` |  |
| Shell | `#89E051` | `.sh` `.bash` `.zsh` |  |
| PowerShell | `#012456` | `.ps1` `.psm1` |  |
| Makefile | `#427819` | `.mk` | `Makefile` `GNUmakefile` |
| Dockerfile | `#384D54` | `.dockerfile` | `Dockerfile` |
| HTML | `#E34C26` | `.html` `.htm` |  |
| CSS | `#563D7C` | `.css` |  |
| SCSS | `#C6538C` | `.scss` |  |
| Less | `#1D365D` | `.less` |  |
| Vue | `#41B883` | `.vue` |  |
| Svelte | `#FF3E00` | `.svelte` |  |
| SQL | `#E38C00` | `.sql` |  |
| HCL | `#844FBA` | `.tf` `.tfvars` `.hcl` |  |
| Objective-C | `#438EFF` | `.m` `.mm` |  |
| Groovy | `#4298B8` | `.groovy` `.gradle` |  |
| Zig | `#EC915C` | `.zig` |  |
| Protocol Buffer | `#6F8FA0` | `.proto` |  |
| TeX | `#3D6117` | `.tex` |  |
| Batchfile | `#C1F12E` | `.bat` `.cmd` |  |
