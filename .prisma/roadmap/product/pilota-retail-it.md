---
{"horizon":"now","id":"OBJ-20366828-5e70-4aba-9c46-1575a1bb7b18","knowledge":["DOC-3e900450-a39c-42b0-a7e1-d6108b0f4363","DOC-258621ff-aafe-4a28-8a49-b7e1c152530e","DOC-3595cb24-e019-4230-a412-009769bae527","DOC-2bb044bc-b64d-4f66-a782-0d25a2866c5d","DOC-d1718775-770d-4065-8b27-3dc7a400dad6","DOC-4a4d6270-cd1a-4748-a1ad-9488cee4ad9e","DOC-1565db4b-4108-490d-aca1-28d30c36ad99","DOC-6db23bbe-fbf2-4913-a207-711865af66f6"],"reopen_reason":"Invito ai tester come testo da copiare (scelta del 2026-10-26).","schema_version":1,"sources":[{"source_id":"SRC-380ef9a9-8ee9-4334-bd62-3af9cb80cda0"}],"title":"Pilota retail.it — ambiente di test su GitStack","updated":"2026-10-26T00:49:54.996415900+00:00"}
---


# Pilota retail.it — ambiente di test su GitStack

## Outcome

Il progetto retail.it (primo repo ospitato ufficialmente) ha su GitStack un ambiente di test: il repo è un mirror in pull da GitHub, la CI di GitStack costruisce le immagini di API, sito e backoffice e le pubblica nel registry del repo, le immagini girano come app GitStack interne accessibili solo ai tester, e le segnalazioni dei tester, con screenshot, diventano issue sul repo aperte dal bot `agent` a nome di chi ha segnalato.

## Rationale

Scelta dell'operatore del 2026-10-26: il pilota corre **in parallelo alla v1.0**, che resta il primo rilascio pubblico con i criteri V1. È il primo cliente reale della v3-a: collaudarla su un caso vero vale più di un collaudo interno. Nessuna data: i tempi li gestisce l'operatore, nel quadro della scelta di chiudere in parallelo tutti gli obiettivi decisi.

## Scope

**Prima parte (P1 della richiesta, serve per il primo rilascio in test)** — tutte coperte da regole decise, manca il codice:

1. CI su un repo in mirror in pull, accesa per repo da chi ha `admin`; ogni sincronizzazione con ref nuovi genera eventi push e tag come un push normale (CI3).
2. Build delle immagini nel runner integrato con BuildKit senza privilegi (CI2).
3. Hosting delle app attivabile dall'installer e da `gitstack upgrade`, con il dominio delle app (H2).
4. Immagini private del registry dello stesso repo usate dalle app senza credenziali a mano (H7, RG1).
5. Segreti delle app passati ai container, mai mostrati, nuova versione a ogni modifica (H8).
6. Login GitStack davanti alle app interne per chi ha `read` sul repo e per chi è nella lista di accesso dell'app, utente in intestazioni firmate (H6).
7. Volumi persistenti delle app e loro backup (v3-b, H3, H4): il PostgreSQL di test di retail.it gira come app con volume nello stesso repo.
8. Nome interno stabile tra app dello stesso repo (H15): la api raggiunge il database come `postgres:<porta>`, stabile tra un deploy e l'altro.
9. Guida CA per i tester: come installare il certificato della CA interna (già su `/downloads`, N5) su Windows, macOS, iOS e Android, con un link da mandare ai tester, così aprono `sito` e `backoffice` senza avvisi del browser (H2). La guida mostra l'**impronta SHA-256** del certificato; l'invito ai tester contiene lo stesso link e la stessa impronta. Nella lista di accesso dell'app, **«Invita i tester»** prepara il testo dell'invito da copiare (link alla guida, impronta, indirizzo dell'app, rimando a «Segnala un problema»); l'admin lo manda con i propri strumenti.
10. Isolamento delle app (H9) e barriere in uscita di H16: internet aperto via proxy se configurato; rete privata (RFC 1918, ULA, CGNAT) **chiusa** senza destinazioni aperte; loopback, cluster, link-local e porte di Kubernetes sempre bloccati. Stesse regole di webhook e mirror (`pkg/egress`, C8).
11. Log e stato delle app (parte di H12): log in diretta e cronologia leggibili da chi ha `write`, stato dell'app e controllo di salute che decide quando una versione è partita.

**Lato retail.it, non GitStack:** il pulsante «Segnala un problema» in `sito` e `backoffice` che apre la issue con screenshot tramite l'API delle issues e il token del bot (vedi Decisioni).

**Seconda parte (P2, subito dopo):** deploy automatico da branch o da regola sui tag e deploy di un tag preciso dalla CI (H7); variabili `GITSTACK_APP_VERSION`, `GITSTACK_COMMIT_SHA`, `GITSTACK_DEPLOYED_AT` (H14); resto di H12: metriche, notifiche, rollback alle versioni precedenti, scala a mano e webhook `deployment`; `gs app` (H13).

**Fuori dal pilota:** del P3 resta fuori Let's Encrypt della v3-c, nel suo obiettivo. Delle regole di H16 restano nella v3-a l'apertura di destinazioni private da parte dell'amministratore, la restrizione dell'uscita per app e la guida. L'account bot non richiede lavoro nuovo (vedi Decisioni); i volumi della v3-b e la guida CA sono entrati nella prima parte.

## Decisioni

Chiuse dall'operatore il **2026-10-26**:

- **Account bot (P3): bastano i grant (P4).** L'amministratore crea un utente `agent` con grant sul solo repo di retail.it e un token con scadenza; nessuna regola nuova. Nota: con `read` su un repo privato il bot legge anche il codice, perché i grant sono per repo e non per azione.
- **PostgreSQL di test: nasce dentro GitStack come app con volume** (v3-b, H3), non sull'host e non su una destinazione privata. Oggi non esiste ancora. Nessuna eccezione a H9; H16 invariata. Per questo la v3-b (volumi e loro backup) entra nella prima parte del pilota.
- **H15 nella prima parte.** Con il database dentro GitStack, il nome interno stabile tra app dello stesso repo serve già al primo rilascio in test: senza, l'indirizzo di `postgres` nel segreto `DATABASE_URL` della api andrebbe aggiornato a mano a ogni deploy. Regola H15 invariata; scartata l'alternativa di lasciarla nella seconda parte.
- **Guida CA per i tester nella prima parte.** Le app interne usano la CA interna (H2): se il dispositivo del tester non se ne fida, il browser mostra un avviso che può fermare chi lavora nel business. La guida non serve se il dominio delle app usa un certificato del cliente già fidato (H2). Scartate: lasciarla fuori e affidare la distribuzione della CA all'amministratore di retail.it; imporre il certificato del cliente.
- **Avviso alla prima apertura della guida, accettato.** La guida sta su GitStack, che usa la stessa CA (H2): il tester vede l'avviso del browser una volta, prima di installare il certificato. Lo rende sicuro il controllo dell'impronta: l'invito contiene link e impronta SHA-256, la guida mostra la stessa impronta e chiede di proseguire e installare solo se coincide. Nessuna regola nuova. Scartate per ora: mandare il certificato nell'invito o da un indirizzo già fidato (da rivalutare se i tester si bloccano sull'avviso); certificato del cliente solo per il dominio delle app.
- **Le issue dei tester passano dal bot.** Chi entra solo dalla lista di accesso non vede le issues (H6), quindi le segnalazioni partono da un pulsante «Segnala un problema» nelle app di test: l'app apre la issue con screenshot tramite l'API delle issues e il token del bot `agent`, conservato come segreto dell'app (H8). La issue risulta aperta da `agent` e riporta chi ha segnalato, preso dalle intestazioni firmate (H6). H6 invariata; il pulsante è codice di retail.it, GitStack non aggiunge nulla. Scartate: un ruolo sul repo ai tester (vedrebbero il codice del repo privato); issue aperte a mano da una persona di retail.it.
- **H16 divisa: barriere nella prima parte, aperture fuori dal pilota.** Le barriere in uscita sono sicurezza e devono esserci dal primo giorno in cui un'app gira, insieme all'isolamento H9, ora scritto in modo esplicito nella prima parte. Con il database dentro GitStack, retail.it non deve raggiungere nessuna destinazione privata: apertura da parte dell'amministratore, restrizione per app e guida restano nella v3-a. Regola H16 invariata. Scartate: tutta H16 nella prima parte; H16 lasciata nella seconda parte (app di test con uscita aperta verso la rete privata).
- **H12 divisa: log e stato nella prima parte.** Con i deploy a mano del primo rilascio in test, chi ha `write` deve poter capire perché un'app non parte senza accedere al server. Metriche, notifiche, rollback, scala e webhook `deployment` restano nella seconda parte; fino ad allora, per tornare indietro si rifa a mano il deploy dell'immagine precedente. Regola H12 invariata. Scartate: tutta H12 nella prima parte; H12 lasciata nella seconda parte (guasti diagnosticabili solo da chi accede al server).
- **Invito ai tester: testo da copiare.** GitStack prepara il testo con link alla guida e impronta, così l'impronta non si ricopia a mano; l'admin lo manda con email o chat aziendali. Non dipende dalla posta di GitStack. Scartate per il pilota: invio per email da GitStack con SMTP (C5), annotato come idea nel Dream Journal; nessun aiuto da GitStack (impronta ricopiata a mano, rischio di errori).

## Source references

- Richiesta retail.it del 2026-10-26 (SRC-380ef9a9-8ee9-4334-bd62-3af9cb80cda0)
- [[knowledge/topics/hosting-app]], [[knowledge/topics/ci-registry-release]], [[knowledge/topics/mirror-in-pull]], [[knowledge/topics/rilascio-v1]], [[knowledge/topics/identita-e-sicurezza]], [[knowledge/topics/installazione-e-deploy]], [[knowledge/topics/issues]], [[knowledge/topics/collegamenti-notifiche-webhook]]
- [[roadmap/product/v3-b-dati]], [[roadmap/product/v3-a-app-interne]]

