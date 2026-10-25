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

`ci-runner-esterno.md` (GIT-278) spiega come registrare, avviare e revocare un runner esterno della CI (download da `/downloads`, token monouso, `runs-on`).

`registry.md` (v2-b) è la guida del registry OCI; la CI, il runner e il dogfooding su homehub (GIT-291) sono in `operations.md` («Built-in CI» e «Dogfooding della CI di GitStack su homehub»), la guida utente pubblica in `docs-site/docs/user/ci.md` e `registry-and-releases.md`.

`webhooks.md` (M-06/A, GIT-129) è il formato dei payload webhook GitStack v1 (push, issues, issue_comment, repository), con intestazioni, firma HMAC-SHA256, tentativi ed esempi; gli eventi di dominio `issue.*`, `issue_comment.*` e `repository.*` che li alimentano sono in `events.md`.

`docs-site/` (GIT-223) è il sito di documentazione pubblico in inglese, generato con Docusaurus: le pagine Admin sono copie di `operations.md`, `versioning.md` e `SECURITY.md` fatte in fase di build, quindi i file in questa cartella restano l'unica fonte. Vedi la sezione «Sito di documentazione» del README di radice.

Licenza: AGPL-3.0, come il resto del server (vedi LICENSE in radice).

`rules-coverage.md` (GIT-115) è la tabella regole di prodotto → test; `scripts/check-rules-coverage.go` ne verifica in CI che i test citati esistano.

`agent-trial.md` (M-07/M, GIT-174) è la guida per il board alla prova reale di un agente di coding con `gs` e le skills, su una VM di prova o su homehub; gli script e il repo di prova sono in `scripts/agent-trial/`.

`release-checklist.md` (M-09/B, GIT-226) è la lista di controllo della release candidate: un passo per criterio di uscita V1; `scripts/release-readiness.go` (`make release-readiness`) verifica gli automatici e stampa i manuali.
