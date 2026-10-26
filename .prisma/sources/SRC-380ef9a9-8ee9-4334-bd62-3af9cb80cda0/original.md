# Richiesta dal progetto retail.it: ambiente di test ospitato su GitStack

(Testo ricevuto dall'operatore il 26/10/2026, incollato in sessione di analisi.)

## Contesto
retail.it è un portale di annunci immobiliari commerciali e industriali. Il repo principale è su GitHub (fathorMB/Retail.it). Vogliamo un ambiente di test su GitStack dove fare le prime pubblicazioni e dove il business possa provare e rivedere il prodotto. Questa è la decisione presa dall'utente il 26/10/2026.

Componenti da ospitare:
- API .NET;
- sito Next.js con rendering lato server;
- backoffice Next.js;
- database PostgreSQL con PostGIS;
- più avanti, la CI dovrà costruire anche un'app Android in Flutter (non da ospitare).

Flusso scelto:
1. Il repo retail.it su GitStack è un mirror in pull da GitHub.
2. La CI di GitStack parte sul mirror.
3. La CI costruisce le immagini e le carica nel registry di GitStack.
4. Le immagini partono come app GitStack, accessibili solo ai tester.
5. Dal sito e dal backoffice di test, i tester aprono issue con screenshot sul repo GitStack.

Abbiamo letto il codice di GitStack al commit 69b6bd3 (25/10/2026). Qui sotto c'è cosa manca per noi, in ordine di priorità. Per ogni punto ci sono il riferimento nel codice e il criterio con cui lo consideriamo fatto. Se un punto è già pianificato con un'altra forma, va bene: diteci quale e quando.

## P1: serve per il primo rilascio in test

1. **CI sui repo in mirror in pull.**
   - Oggi i workflow sono bloccati sui mirror (services/core/internal/ci/policy.go, ReasonPullMirror). L'interruttore core.ci_repo_state.run_on_pull_mirror esiste solo nel database.
   - Chiediamo: un'impostazione per repo, da interfaccia (Impostazioni → CI) e da API, che abiliti la CI sul mirror.
   - Chiediamo anche che ogni sincronizzazione che porta nuovi commit o tag generi gli eventi push e tag, come un push normale.
   - Fatto quando: un tag v0.1.0-test.1 creato su GitHub, dopo la sincronizzazione del mirror, avvia il workflow in .gitstack/workflows/.

2. **Costruzione di immagini Docker in CI.**
   - Oggi i runner integrati non fanno docker build (docs/adr/0001, il tipo di job "build" non è collegato).
   - Chiediamo una delle due strade: il collegamento del job di build con BuildKit rootless, oppure una guida e un runner esterno supportati (gitstack-runner con Docker/buildx), con un esempio di workflow che costruisce e carica nel registry del repo usando il job token.
   - Fatto quando: un workflow costruisce tre immagini (api, sito, backoffice) con tag sha-<commit> e vX.Y.Z-test.N, e le carica in <host>/<owner>/retail-it/<nome>.

3. **Hosting delle app abilitato dall'installazione.**
   - Oggi GITSTACK_CORE_APPS_* non sono impostate dal chart Helm e il core non ha i permessi per gestire namespace e deployment.
   - Chiediamo: i template apps-* nel chart, con ClusterRole e RBAC, attivabili con install.sh --apps (o gitstack upgrade --set apps.enabled=true) e con il dominio delle app.
   - Fatto quando: su un'istanza installata dallo script si crea un'app e si fa un deploy senza configurazioni manuali.

4. **Immagini private del registry del repo.**
   - Oggi apps_wiring.go non imposta mai PullCredentials, quindi un'app non scarica le immagini private del proprio repo.
   - Fatto quando: un'app usa un'immagine privata del registry dello stesso repo senza credenziali inserite a mano.

5. **Segreti delle app passati ai container.**
   - Oggi i segreti sono salvati (/apps/{app}/secrets) ma non arrivano al pod: in services/core/internal/apps/manifests.go, envVars legge solo le variabili.
   - Ci servono per la stringa di connessione al database, le chiavi delle API Google e il token del bot che apre le issue.
   - Chiediamo: segreti iniettati come variabili d'ambiente da un Secret Kubernetes, mai mostrati in chiaro nell'interfaccia e nelle API.
   - Fatto quando: un segreto DB_CONNECTION impostato su un'app è leggibile dal processo nel container, e una sua modifica crea una nuova versione e riavvia l'app.

6. **Accesso alle app riservato ai tester (modalità "login").**
   - Oggi le app con accesso "login" non hanno un indirizzo senza il middleware forward-auth (controller.go:353), e /apps/{app}/access è uno stub (501).
   - Chiediamo il disegno H6 dell'ADR 0002: forward-auth del gateway; passano gli utenti con lettura sul repo; l'utente arriva all'app in header firmati X-GitStack-*; endpoint access funzionante.
   - Chiediamo anche un modo per dare accesso a tester esterni, cioè persone del business senza altri permessi: per esempio un ruolo "solo lettura app" o un invito a un gruppo.
   - Fatto quando: un utente GitStack con lettura sul repo apre https://sito-retail-it-<owner>.<dominio app> e vede il sito; un utente senza permessi viene mandato al login e poi riceve un rifiuto.

## P2: serve presto, per lavorare bene

7. **Deploy automatico a ogni nuova immagine.** followBranch è salvato ma nulla lo attiva. Chiediamo il deploy automatico quando nel registry arriva un'immagine con un tag che rispetta una regola (es. v*-test.*), e un comando o un'API usabile dalla CI per fare il deploy di un tag preciso. Fatto quando: un tag di test su GitHub porta, senza passi manuali, alla nuova versione online.
8. **Versione e commit visibili all'app.** Oggi al container arriva solo PORT. Chiediamo GITSTACK_APP_VERSION (tag dell'immagine), GITSTACK_COMMIT_SHA (dall'etichetta OCI org.opencontainers.image.revision o dalla versione dell'app) e GITSTACK_DEPLOYED_AT. Serve per la barra di test che mostra la versione.
9. **Collegamento tra le app dello stesso repo.** Un nome interno stabile e documentato (es. api.retail-it.internal) o una variabile iniettata con l'indirizzo delle app sorelle; una NetworkPolicy che permetta il traffico tra app dello stesso repo e lo blocchi verso le altre.
10. **Uscita verso servizi esterni.** PostgreSQL esterno a GitStack, Google Maps, Azure Communication Services, Firebase. Documentare come si abilita l'uscita, eventualmente un elenco di destinazioni consentite per app.
11. **Log e stato delle app.** Oggi i log sono uno stub. Almeno: log del container in interfaccia, stato del deploy, webhook o notifica "deployment" su successo, fallimento e ritorno automatico.
12. **CLI per le app.** gs app list, deploy, rollback, logs e vars/secrets.

## P3: utile, non bloccante

13. **Segnalazioni dei tester come issue.** Un tipo di account "bot" con token che possa solo aprire issue e caricare allegati su un repo, con etichette e template; conferma che gli allegati immagine si vedono in anteprima nella issue; webhook issues con cambi di stato ed etichette.
14. **TLS e DNS per i tester del business.** Guida per distribuire la CA ai tester, o meglio Let's Encrypt per le app (v3-c) appena possibile.
15. **Volumi persistenti (v3-b).** Quando ci saranno, valuteremo di portare su GitStack anche il PostgreSQL di test.

## Cosa ci serve sapere da voi
- Quali punti sono già in roadmap, con quale forma e con quale data indicativa (soprattutto i P1).
- Se l'ADR 0002 (hosting delle app) è approvato o cambierà.
- Se per la build delle immagini consigliate BuildKit integrato o runner esterno.
- Un contatto o un item in cui seguire l'avanzamento.
