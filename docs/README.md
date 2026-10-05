# docs/

Documentazione rivolta a chi installa e usa GitStack (setup, guide, riferimento API), distinta dalla knowledge base interna per gli agenti in `.lmbrain-lite/knowledge/` (architettura, decisioni, visione).

Stato: prime pagine tecniche condivise tra servizi. `events.md` documenta le
convenzioni di nomi di subject/stream NATS JetStream e l'envelope degli
eventi (`pkg/events`). `dev-environment.md` (M-01/T-10) documenta l'ambiente
di sviluppo locale riproducibile (`make dev-up`/`dev-down`/`dev-redeploy`,
cluster k3d): prerequisiti per Linux/macOS/Windows-WSL2 e il ciclo modifica
→ rebuild → redeploy di un servizio. `identity-oidc.md` (M-02/K) documenta il login esterno OIDC: configurazione, regole di collegamento degli utenti ed esempi per Entra ID, Google e Keycloak. Per il resto, fino a quando non ci sono
altre guide utente dedicate, il riferimento è
`.lmbrain-lite/knowledge/architecture.md` e
`.lmbrain-lite/knowledge/vision.md`.

`repos.md` (M-03/A) documenta scope, owner/visibilità e API interna dei repo.
`issue-templates.md` (M-05/H, GIT-108) documenta i modelli di issue in
`.gitstack/ISSUE_TEMPLATE/`: formato, front matter, comportamento e
contratto API.

Licenza: AGPL-3.0, come il resto del server (vedi LICENSE in radice).

`rules-coverage.md` (GIT-115) è la tabella regole di prodotto → test; `scripts/check-rules-coverage.go` ne verifica in CI che i test citati esistano.
