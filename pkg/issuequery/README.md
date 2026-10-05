# pkg/issuequery

Sintassi di ricerca delle issues (regola I10): la stessa in UI, API e `gs`.
Il pacchetto trasforma una stringa in una `Query` tipizzata; non accede al
database (la traduzione in SQL è di chi lo usa).

```go
q, err := issuequery.Parse(`is:open label:"good first issue" -assignee:@me crash`)
var pe *issuequery.Error
if errors.As(err, &pe) { /* pe.Pos = offset in byte, pe.Msg = messaggio */ }
```

## Grammatica

```
query      = { spazio | token }
token      = [ "-" ] qualifier ":" value | [ "-" ] value
value      = "\"" { carattere | "\\\"" | "\\\\" } "\""   (fra virgolette)
           | { carattere non spazio }
```

Una chiave è una sequenza di lettere ASCII subito seguita da `:` (non
distingue maiuscole). Tutto il resto è testo libero. I qualificatori ripetuti
si accumulano nell'ordine di scrittura.

| Qualificatore | Valori | Campo |
|---|---|---|
| `is:` | `open`, `closed`, `issue` | `Is` |
| `reason:` | `completed`, `not-planned`, `duplicate` | `Reasons` |
| `label:` | nome, anche `"con spazi"` | `Labels` |
| `assignee:` | login (con o senza `@`), `@me`, `@agents` | `Assignees` |
| `author:` | come `assignee:` | `Authors` |
| `milestone:` | nome, anche fra virgolette | `Milestones` |
| `no:` | `label`, `assignee`, `milestone` | `No` |
| `repo:` | `owner/repo` | `Repos` |
| `org:` | nome dell'organizzazione | `Orgs` |

Testo libero: parole separate da spazi o frasi fra virgolette (`Term.Quoted`),
in `Text`; `FreeText()` unisce i termini non negati.

## Negazione

`-` davanti a un qualificatore (`-label:wontfix`, `-is:closed`, `-no:label`) o a
un termine di testo (`-foo`, `-"frase"`) imposta `Negated`. Un `-` dentro una
parola (`foo-bar`) è testo. Il significato di più condizioni dello stesso tipo
(AND/OR) lo decide chi traduce la query.

## Errori

`Parse` si ferma al primo errore e restituisce `*Error{Pos, Msg}`; `Pos` è
l'offset in byte nella stringa: l'inizio del token per un qualificatore
sconosciuto o un `-` isolato, l'inizio del valore per un valore non valido o
mancante, la virgoletta di apertura per virgolette non chiuse. Un testo come
`http://x` è un qualificatore sconosciuto: va messo fra virgolette.

## Esempi

| Stringa | Significato |
|---|---|
| `is:open label:bug` | issue aperte con etichetta bug |
| `label:bug label:ui` | due condizioni sulle etichette |
| `assignee:@me -label:wontfix` | assegnate a me, senza wontfix |
| `is:closed reason:not-planned` | chiuse come non pianificate |
| `no:assignee no:milestone` | senza assegnatario né milestone |
| `repo:acme/web author:@agents` | nel repo, aperte da agenti |
| `org:acme "null pointer"` | nell'organizzazione, frase esatta |
