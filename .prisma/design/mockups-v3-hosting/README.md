# Mockups · v3 Hosting (app e container)

Schermate della v3 (regole H1–H13 in `knowledge/topics/hosting-app.md`). Stesso stile e stessa barra di navigazione di `mockups-v1` e `mockups-v2-build`: la barra scura in alto e le barre delle varianti non fanno parte del prodotto.

| # | Schermata | Passo | Regole |
|---|-----------|-------|--------|
| H1 | Apps del repo: stato, indirizzo, accesso, versione, branch seguito, copie, verificata o no; variante repo senza app | v3-a | H1, H2, H5, H6, H10 |
| H2 | New app / Deploy (varianti: immagine del repo firmata, immagine esterna non verificata, server pieno) | v3-a | H2, H7, H9, H10, H11 |
| H3 | App · Overview (varianti: in esecuzione, deploy fallito con ritorno automatico): salute, metriche, garantito e tetto, copie | v3-a | H7, H11, H12 |
| H4 | App · Logs in diretta e cronologia, eventi della piattaforma, segreti mascherati | v3-a | H8, H12 |
| H5 | App · Versions e rollback (variante conferma) | v3-a | H4, H8, H12, H13 |
| H6 | App · Settings · Variables &amp; secrets | v3-a | H8 |
| H7 | App · Settings · Access &amp; domains (varianti: interna con pubbliche non abilitate; pubblica con dominio proprio) | v3-a, v3-c | H1, H2, H6 |
| H8 | App · Settings · Volumes &amp; resources: volumi nei backup, avviso database, copia unica, restricted | v3-b | H3, H9, H11 |
| H9 | Admin · Apps &amp; resources: capacità del server, quote per owner, garantito/uso/tetto, impostazioni d'istanza | v3-a | H1, H2, H9, H10, H11 |
| H10 | Terminale e MCP: `gs app deploy/logs/rollback/scale`, JSON, strumenti MCP | v3-a | H7, H12, H13 |

Dati finti: organizzazione "Acme", repo interno `acme/handbook` con le app `web`, `search-worker` e `wiki` (immagine Docker Hub non firmata, con volumi), dominio delle app `apps.acme.it`, agente `build-agent`.
