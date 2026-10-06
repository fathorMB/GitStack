# pkg/names

Validazione dei nomi: regola R11 dei repository e nomi riservati per utenti e
organizzazioni (che condividono un unico spazio di nomi). Usato da identity
(M-03/C) e core (M-03/E).

## ValidateRepoName (R11)

- lettere `a-z` e `A-Z`, cifre, `-`, `_` e `.`;
- da 1 a 100 caratteri;
- non inizia con `.`;
- non finisce con `.git` (in qualsiasi combinazione di maiuscole);
- le maiuscole sono conservate come scritte: l'unicità per owner e la ricerca non le distinguono (GIT-178, indice su `lower(name)` in core).

Ogni regola ha il suo errore esportato, da confrontare con `errors.Is`:
`ErrEmpty`, `ErrTooLong`, `ErrInvalidChar`, `ErrLeadingDot`, `ErrGitSuffix`.

## Nomi riservati

`IsReservedOwnerName` (insensibile alle maiuscole) e `ReservedOwnerNames()`
(copia ordinata) coprono i percorsi web di primo livello:

`_components`, `admin`, `api`, `assets`, `change-password`, `explore`,
`healthz`, `login`, `logout`, `new`, `notifications`, `orgs`, `readyz`,
`repos`, `settings`, `static`, `user`, `users`, `v1`.

Il controllo di unicità cross-tabella tra `identity.users.username` e
`identity.organizations.name` resta a carico dei servizi.
