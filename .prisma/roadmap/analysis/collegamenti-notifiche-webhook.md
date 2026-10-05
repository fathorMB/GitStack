---
{"depends_on":["TOP-4a11694d-e261-4934-bafb-f17ccc6d1726","TOP-74180f5f-ae9e-4bbf-b220-4b52950e5038","TOP-5dd4b056-b64a-4878-9266-6dc2e134dd00"],"id":"TOP-062637bf-9acd-47cb-a089-3fb3bccee486","knowledge":["DOC-f8d2e48c-74fd-4b35-8551-80a2f345eacd","DOC-951cc9b3-4a06-48b1-959e-02a9431bbbb2","DOC-1565db4b-4108-490d-aca1-28d30c36ad99","DOC-6878277b-230e-448b-a761-215e9d233c54","DOC-6db23bbe-fbf2-4913-a207-711865af66f6"],"schema_version":1,"state":"consolidated","title":"Collegamenti, notifiche e webhook: regole di prodotto","updated":"2026-10-05T09:10:00+00:00"}
---

# Collegamenti, notifiche e webhook: regole di prodotto

## Expected learning

Regole per M-06 oltre a D10 e D11: come si collegano commit e issues, chi riceve quali notifiche e come, come funzionano i webhook verso sistemi esterni.

## Già deciso (da altri temi)

- D10: collegamenti `#12` e `fixes #12`, notifiche in-app ed email, webhook. D11 e convenzioni eventi: `git.push` pubblicato dal servizio git, envelope versionato.
- Le chiusure via commit valgono sul branch principale (R4) e chiudono come *completata* (I2).
- Le menzioni notificano solo chi vede il repo, team compresi (I8). Numerazione `#n` unica per repo (I1).
- Mockup: 05 Notifications, 11 Settings · Webhooks (8 tentativi in 24 ore, firma HMAC-SHA256), 13 Issue detail.

## Scelte confermate dall'operatore (2026-10-05)

- **C1 — Riferimenti tra repo con nome completo:** `#12` indica il repo corrente, `owner/repo#12` un altro repo. I riferimenti diventano link e la issue citata mostra "referenced from …" solo a chi vede entrambi i repo (P3). `fixes owner/repo#12` chiude la issue solo se **chi ha fatto il push** ha `write` sul repo della issue.
- **C2 — Parole chiave di GitHub, chiusura sul branch principale:** `close|closes|closed`, `fix|fixes|fixed`, `resolve|resolves|resolved` (senza distinzione di maiuscole) seguite da `#n` o `owner/repo#n`. Un commit su qualsiasi branch crea solo il collegamento ("referenced this issue"); la issue si chiude come *completata* (I2) quando il commit entra nel branch principale (R4) via push diretto o, dalla v1.1, merge di una PR, con traccia "closed by commit <sha>". Riaprire una issue non la fa richiudere dallo stesso commit. Parole in italiano escluse (eventualmente in seguito).
- **C3 — Partecipanti in automatico, Watch a tre livelli:** seguono una issue in automatico autore, assegnatari, chi commenta e chi viene menzionato (I8); "Subscribe/Unsubscribe" per cambiare. Notificati: commenti, chiusura e riapertura, assegnazioni, commit collegati. Watch del repo: *Partecipando* (default), *Tutto* (ogni nuova issue e commento), *Ignora*. Nessuna notifica per le proprie azioni, anche fatte con i propri token.
- **C4 — Agenti con la stessa casella delle persone:** gli agenti ricevono le notifiche con le stesse regole (C3) e le leggono via API e `gs notification list --json`, con filtro per motivo (`assigned`, `mentioned`, …) e segnatura come lette. Nessuna email agli agenti. Per reagire subito si usano i webhook. Le skills (M-07) insegnano il ciclo leggi notifiche → lavora → rispondi.
- **C5 — Email facoltative, immediate, per tipo:** senza SMTP configurato GitStack funziona con le sole notifiche in-app (nessun blocco all'installazione). Con SMTP, ogni persona sceglie per tipo (menzioni, assegnazioni, attività sulle issue seguite) se ricevere anche l'email; default: email per menzioni e assegnazioni, solo in-app per il resto. Invio immediato con breve raggruppamento (pochi secondi) degli eventi della stessa issue. Email con link alla issue; nessuna risposta via email nella v1.
- **C6 — Webhook in formato GitStack documentato:** webhook di repo (configurati da chi ha `admin` sul repo) e di organizzazione (configurati dagli owner, valgono per tutti i repo dell'organizzazione). Eventi selezionabili: `push`, `issues`, `issue_comment`, `repository` (creato, archiviato, eliminato, ripristinato) (mockup 11). Payload in formato **proprio**, con nomi e struttura ispirati a GitHub (es. `push` con `ref`, `before`, `after`, `commits`, `repository`, `sender`), descritto nella documentazione API e versionato; header `X-GitStack-Event`, `X-GitStack-Delivery`, `X-GitStack-Signature` (HMAC-SHA256). Nessuna promessa di compatibilità con GitHub nella v1; una variante "compatibile GitHub" per `push` resta un'estensione possibile.
- **C7 — Consegna con tentativi crescenti e disattivazione automatica:** su errore 5xx, errore di rete o timeout (10 s) fino a 8 tentativi in circa 24 ore con attesa crescente; una risposta `410 Gone` ferma subito i tentativi della consegna. Dopo 3 giorni di fallimenti consecutivi il webhook si disattiva da solo e chi lo gestisce (admin del repo o owner dell'organizzazione) riceve una notifica. Log delle consegne (richiesta, risposta, durata, "Redeliver") conservato 30 giorni.
- **C8 — Destinazioni dei webhook (protezione SSRF):** ammessi gli indirizzi della rete aziendale (caso d'uso principale on-prem). Sempre bloccati, anche dopo redirect e risoluzione DNS: `localhost`/loopback, indirizzi interni del cluster k3s (servizi GitStack, API Kubernetes) e link-local (`169.254.0.0/16`, es. metadata). L'amministratore dell'installazione può definire liste di destinazioni ammesse e vietate per regole più strette. Nel log la risposta è troncata ai primi 4 KB.
- **C9 — Conservazione delle notifiche:** le notifiche lette si eliminano dopo 90 giorni; quelle non lette restano finché non vengono lette. Se il destinatario perde l'accesso al repo, le notifiche di quel repo spariscono dalla sua casella (P3). Lettura, archiviazione ed eliminazione manuali anche via API e `gs`.

## Open questions

1. ~~Riferimenti tra repo~~ (C1).
2. ~~Chiusura via commit~~ (C2).
3. ~~Chi segue cosa~~ (C3).
4. ~~Notifiche agli agenti~~ (C4).
5. ~~Email~~ (C5).
6. ~~Webhook: ambito, eventi, formato~~ (C6).
7. ~~Consegna dei webhook~~ (C7).
8. ~~Destinazioni dei webhook~~ (C8).
9. ~~Conservazione delle notifiche~~ (C9).

## Consolidation summary

Consolidato il 2026-10-05 con conferma dell'operatore. Regole C1–C9: riferimenti `owner/repo#n` visibili solo a chi vede entrambi i repo e chiusure solo con `write` di chi fa push; parole chiave di GitHub con chiusura all'arrivo sul branch principale; partecipanti che seguono in automatico e Watch a tre livelli; agenti con la stessa casella via API e `gs`; email facoltative, immediate, per tipo; webhook di repo e organizzazione in formato GitStack documentato; consegna con tentativi crescenti e disattivazione automatica; protezione SSRF con rete aziendale ammessa e interni bloccati; notifiche lette conservate 90 giorni. Riportato in [[knowledge/topics/collegamenti-notifiche-webhook]], nell'obiettivo M-06, in D10 e in [[knowledge/topics/installazione-e-deploy]]. Mockup 05, 07, 11, 13 da aggiornare e schermata "Notification settings" da aggiungere.
