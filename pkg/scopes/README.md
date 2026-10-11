# pkg/scopes

Unica fonte della regola di implicazione tra scope dei token (GIT-190):

- `write:X` implica `read:X`;
- `admin:X` implica `write:X` e `read:X`.

`Grants(have, required)` è usata dal gateway (scope delle rotte API), da
identity (`tokens.Satisfies`) e da git (`access`, HTTPS e SSH). Nessun servizio
ne tiene una copia: per cambiare la regola si modifica solo questo modulo.
