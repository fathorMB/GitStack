# Mockups · v3 Hosting (app e container)

Schermate della v3 (regole H1–H16 in `knowledge/topics/hosting-app.md`; H2–H10 riallineate il 2026-10-26 a H6, H7, H14 e H16). Stesso stile e stessa barra di navigazione di `mockups-v1` e `mockups-v2-build`: la barra scura in alto e le barre delle varianti non fanno parte del prodotto.

| # | Schermata | Passo | Regole |
|---|-----------|-------|--------|
| H1 | Apps del repo: stato, indirizzo, accesso, versione, branch seguito, copie, verificata o no; variante repo senza app | v3-a | H1, H2, H5, H6, H10 |
| H2 | New app / Deploy (varianti: immagine del repo firmata, immagine esterna non verificata, server pieno); deploy a mano, da branch o da regola sui tag | v3-a | H2, H7, H9, H10, H11 |
| H3 | App · Overview (varianti: in esecuzione, deploy fallito con ritorno automatico): salute, metriche, garantito e tetto, copie | v3-a | H7, H11, H12 |
| H4 | App · Logs in diretta e cronologia, eventi della piattaforma, segreti mascherati | v3-a | H8, H12 |
| H5 | App · Versions e rollback (variante conferma) | v3-a | H4, H8, H12, H13 |
| H6 | App · Settings · Variables &amp; secrets, variabili di sistema `GITSTACK_` e `PORT` | v3-a | H8, H14, H15 |
| H7 | App · Settings · Access &amp; domains (varianti: interna con lista di accesso e pubbliche non abilitate; pubblica con dominio proprio) | v3-a, v3-c | H1, H2, H6 |
| H8 | App · Settings · Volumes &amp; resources: volumi nei backup, avviso database, copia unica, restricted, uscita dell'app | v3-b | H3, H9, H11, H16 |
| H9 | Admin · Apps &amp; resources: capacità del server, quote per owner, garantito/uso/tetto, impostazioni d'istanza con uscita e destinazioni private | v3-a | H1, H2, H9, H10, H11, H16 |
| H10 | Terminale e MCP: `gs app deploy/logs/rollback/scale`, JSON, strumenti MCP | v3-a | H7, H12, H13 |
| H11 | Pilota retail.it (varianti: Apps del repo in mirror con `api`, `sito`, `backoffice` e `postgres`; postgres · Volumes &amp; backup con dump, ultimo backup e rete) | Pilota, v3-a, v3-b | H3, H4, H5, H6, H9, H15, H16 |
| H12 | Guida CA per i tester, senza barra laterale (varianti: Windows, macOS, iPhone/iPad, Android, prova riuscita): download da `/downloads`, impronta da confrontare, passi, «Prova» su `sito` | Pilota | H2, N5 |
| H13 | Admin di retail.it su `sito` (varianti: lista di accesso dei tester; invito con link alla guida e impronta, testo da copiare; segreto `ISSUE_BOT_TOKEN` del bot e giro di «Report a problem») | Pilota | H6, H8, P4, I3, I9 |

Dati finti: organizzazione "Acme", repo interno `acme/handbook` con le app `web`, `search-worker` e `wiki` (immagine Docker Hub non firmata, con volumi), dominio delle app `apps.acme.it`, agente `build-agent`, destinazione privata aperta `smtp.acme.lan:587`. Per H11: repo privato `retail/retail-it` in mirror da GitHub, dominio `apps.gitstack.lan`, PostgreSQL di test come app con volume (scelta del 2026-10-26), tester nella lista di accesso, bot `agent`.
