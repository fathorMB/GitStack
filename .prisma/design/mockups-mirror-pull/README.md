# Mockups · Mirror in pull

Schermate del mirror in pull (regole MP1–MP7, `knowledge/topics/mirror-in-pull.md`), primo pezzo di M-09. Stesso stile e stessa barra di navigazione di `mockups-v1`: la barra scura in alto e le barre delle varianti non fanno parte del prodotto.

| # | Schermata | Regole |
|---|-----------|--------|
| P1 | New repository · Import from URL (varianti: origine valida, origine rifiutata dalla policy di uscita, import una tantum) | MP3, MP4, MP5, MP8 |
| P2 | Repo in mirror · Code (varianti: attivo, in pausa, ref diverged, fermo da più di 24 ore) | MP1, MP2, MP4, MP6 |
| P3 | Settings · Pull mirror (varianti: attivo, in pausa, conferma di Stop mirroring) | MP1, MP3, MP4, MP6 |
| P4 | Settings · Enable pull mirror su un repo esistente con contenuti | MP1, MP3 |
| P5 | Settings · Push mirrors: blocco verso lo stesso indirizzo dell'origine e tappa 2 di V7 | MP6 |
| P6 | Notifications del mirror (ref diverged, nessun aggiornamento da 24 ore) | MP1, MP4 |
| P7 | Terminale: push rifiutato e `gs repo view` | MP2, MP7 |

Dati finti: organizzazione "Acme", repo `acme/GitStack` copiato da `github.com/acme-labs/GitStack`, istanza `homehub.local`.
