---
{"depends_on":["TOP-e3e42f34-b1db-4b54-8b6d-3a4f1a2845a5"],"id":"TOP-9b160889-2953-4eaf-b316-1fb22dc8f061","knowledge":["DOC-4a4d6270-cd1a-4748-a1ad-9488cee4ad9e","DOC-f8d2e48c-74fd-4b35-8551-80a2f345eacd"],"schema_version":1,"state":"consolidated","title":"Supporto Windows via WSL2 in produzione","updated":"2026-10-05T13:20:00+00:00"}
---

# Supporto Windows via WSL2 in produzione

## Expected learning

Capire se e come offrire un supporto ufficiale affidabile su Windows (D14), visto il rischio noto di WSL2 in produzione.

## Già deciso (da altri temi)

- D14: Windows via WSL2 previsto; rischio noto di WSL2 in produzione.
- N2: sistemi supportati nella v1 Ubuntu Server e Pop!_OS 22.04/24.04 amd64; N3 un solo nodo; N5 HTTPS con CA interna; R7 SSH sulla porta 2222.

## Scelte confermate dall'operatore (2026-10-05)

- **W1 — Windows "di prova" con limiti dichiarati:** l'installer gira in WSL2 con Ubuntu 22.04 o 24.04 (N2); uno script PowerShell prepara Windows (attiva WSL2, installa Ubuntu, lancia l'installer). Indicato per prove, demo e uso personale o di un piccolo team. La documentazione dichiara che per server condivisi va usato Ubuntu Server (anche in una VM). Nessuna garanzia di disponibilità; in CI solo un test di installazione, nessun test di riavvio o aggiornamento di Windows. Il supporto "ufficiale" su Windows resta un candidato dopo la v1.
- **W2 — Avvio al login, accesso locale, rete locale facoltativa:** lo script PowerShell crea un'attività di Windows che avvia WSL2 e GitStack al login dell'utente (nessun servizio prima del login). Di default GitStack risponde solo sul PC (`https://localhost`, SSH sulla porta 2222). Con `-ShareOnNetwork` lo script attiva la rete "mirrored" di WSL2 (Windows 11 22H2 o successivo) e apre le porte 443 e 2222 nel firewall, così i colleghi della rete locale lo raggiungono con il nome del PC senza dipendere dall'IP interno di WSL2. Su Windows 10 l'opzione non è disponibile e lo script lo dichiara.

## Open questions

1. ~~Livello di supporto~~ (W1).
2. ~~Avvio e rete~~ (W2).
3. ~~Test dedicati~~ (W1: solo test di installazione).

## Consolidation summary

Consolidato il 2026-10-05 con conferma dell'operatore. Windows è supportato "di prova" con limiti dichiarati (W1): WSL2 con Ubuntu 22.04/24.04 preparato da uno script PowerShell, per prove, demo e piccoli team; per server condivisi Ubuntu Server; in CI solo un test di installazione. Avvio al login, accesso solo dal PC e rete locale facoltativa con `-ShareOnNetwork` tramite rete mirrored su Windows 11 (W2). D14 cambia da "supporto ufficiale" a "di prova" (versione precedente tracciata come superata). Riportato in [[knowledge/topics/decisioni]], [[knowledge/topics/installazione-e-deploy]], nell'obiettivo M-08 e in PROJECT.md.
