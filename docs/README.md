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

Licenza: AGPL-3.0, come il resto del server (vedi LICENSE in radice).
