# Regole di prodotto → test

Tabella di copertura richiesta dal criterio V1 di `.prisma/knowledge/topics/rilascio-v1.md`: per la v1.0 ogni regola di prodotto decisa è coperta da almeno un test automatico. Creata con GIT-115 il 2026-10-05.

## Come si legge e come si aggiorna

- **Regola**: id e frase breve; il testo completo sta nel topic indicato sopra ogni famiglia.
- **Test**: ogni test è un riferimento tra apici inversi, `percorso/del_file#Nome`, relativo alla radice del repo.
  - Go: `file_test.go#TestNome` oppure `file_test.go#TestNome/sottotest` (sottotest scritto come nel nome Go, con i trattini bassi). Se i casi sono righe di una tabella e non sottotest, si cita il solo `TestNome`.
  - Web: `file.test.tsx#titolo del test` (il titolo, o un suo pezzo senza apostrofi, backtick e `|`).
- **Stato**: `coperta`, `parziale`, `non coperta`, `n/a, motivata` (solo per esclusioni di prodotto) oppure `da compilare` (famiglia non ancora riempita).
- **Cosa manca**: obbligatorio per `parziale` e `non coperta`. Lo colma chi chiude la milestone o un item di correzione aperto dal CTO.
- Chi aggiunge un test per una regola aggiorna la riga nello stesso item. Chi chiude una milestone riempie la famiglia (M-05: GIT-113).
- Il controllo `go run scripts/check-rules-coverage.go` (CI, job Go, e check `rules-coverage` del motore) verifica che ogni test citato esista ancora nel file indicato e che ogni riga `coperta` o `parziale` ne citi almeno uno.
- Tutti i test citati vivono nel codice su main; alcuni girano solo con `-tags=integration` o con Postgres/Docker (vedi `.github/workflows/ci.yml`): il controllo verifica l'esistenza, non l'esito.

## R — Repository Git (`repository-git.md`)

| Id | Regola | Test | Stato | Cosa manca |
|---|---|---|---|---|
| R1 | Indirizzi `https://<host>/<owner>/<repo>.git` e `/<owner>/<repo>`; spazio di nomi unico utenti/organizzazioni, nomi riservati | `services/core/internal/stackitest/git_integration_test.go#TestGitClientReale/https_clone_push_pull`<br>`services/identity/internal/httpapi/owner_visibility_integration_test.go#TestSharedNamespaceAndReservedNames`<br>`web/src/pages/repos/Repos.test.tsx#repo non vuoto: niente quick setup` | parziale | La pagina web `/<owner>/<repo>` è provata con un router di test, non con le rotte reali di `App.tsx`; nessun test del nome breve `owner/repo` come argomento di `gs` (M-06). |
| R2 | Eliminazione recuperabile 7 giorni: sparisce subito, nome occupato, poi purge | `services/core/internal/httpserver/repos_trash_integration_test.go#TestRepos_EliminazioneERipristino/elimina_sparisce_subito`<br>`services/core/internal/httpserver/repos_trash_integration_test.go#TestRepos_EliminazioneERipristino/nome_occupato`<br>`services/core/internal/httpserver/repos_trash_integration_test.go#TestRepos_EliminazioneERipristino/scaduto_non_si_ripristina`<br>`services/core/internal/repopurge/repopurge_integration_test.go#TestJob_CancellaSoloOltreSetteGiorni`<br>`services/core/internal/repopurge/repopurge_integration_test.go#TestJob_NomeLiberoSoloDopo`<br>`services/core/internal/stackitest/git_integration_test.go#TestGitClientReale/repo_eliminato` | coperta | |
| R3 | Nessuna rinomina né trasferimento nella v1 (esclusione); owner ben visibile alla creazione | `services/core/internal/httpserver/repos_trash_integration_test.go#TestRepos_EliminatoNelleApiGeneriche/repo_vivo_non_modificabile_da_resources`<br>`services/core/internal/httpserver/repos_integration_test.go#TestRepos_Impostazioni/corpo_non_valido`<br>`web/src/pages/repos/Repos.test.tsx#owner ben in vista con la nota R3` | parziale | Nessun test prova che `PATCH /repos/{owner}/{repo}` rifiuta il campo `name`/`owner` (oggi `corpo_non_valido` prova solo un campo sconosciuto `nome`). |
| R4 | Branch principale `main`, modificabile da chi ha `admin` tra i branch esistenti; è quello mostrato dal browser | `services/git/internal/repostore/repostore_test.go#TestCreate_BranchPersonalizzato`<br>`services/core/internal/httpserver/repos_integration_test.go#TestRepos_Impostazioni/branch_principale_fra_quelli_esistenti`<br>`services/core/internal/httpserver/repos_integration_test.go#TestRepos_Impostazioni/senza_permesso`<br>`web/src/pages/repos/RepoSettings.test.tsx#salva branch principale e protezione` | coperta | |
| R5 | Contenuto iniziale opzionale (README, `.gitignore`, licenza), spento di default; primo commit su `main` | `services/git/internal/repostore/repostore_test.go#TestCreate_Vuoto`<br>`services/git/internal/repostore/repostore_test.go#TestCreate_PrimoCommit`<br>`services/git/internal/templates/templates_test.go#TestLicensePlaceholdersFilled`<br>`services/core/internal/httpserver/repos_integration_test.go#TestRepos_Creazione/visibilita_interna_e_contenuto_iniziale`<br>`web/src/pages/repos/Repos.test.tsx#crea con i default: private, readme false, nessun modello` | parziale | Manca la parità con `gs repo create` (M-06, G4): oggi provati solo API e UI. |
| R6 | Push rifiutato oltre 100 MB; avviso oltre 5 GB; soglie impostabili dall'amministratore | `services/core/internal/stackitest/git_integration_test.go#TestGitClientReale/R6_file_oltre_100MB`<br>`services/git/internal/smarthttp/receive_test.go#TestPushFileOltreLaSogliaRifiutato`<br>`services/git/internal/smarthttp/receive_test.go#TestPushSogliaConfigurabile`<br>`services/git/internal/smarthttp/receive_test.go#TestPushRepoGrandeAvvisaMaAccetta`<br>`services/git/internal/sshd/receive_test.go#TestPushFileOltreLaSogliaViaSSH`<br>`services/git/internal/config/limits_test.go#TestLoad_SoglieDaAmbiente` | coperta | |
| R7 | SSH sulla porta 2222 di default, configurabile; indirizzo completo, forma corta solo con porta 22 | `services/core/internal/httpserver/repos_clone_test.go#TestCloneConfig_R7`<br>`services/core/internal/httpserver/repos_clone_test.go#TestCloneConfig_SenzaPublicURL`<br>`services/core/internal/stackitest/git_integration_test.go#TestGitClientReale/ssh_clone_push_pull`<br>`services/git/internal/config/config_test.go#TestLoad_SSH`<br>`web/src/pages/repos/CodeBrowser.test.tsx#menu Clone: SSH con porta 2222 e con porta 22` | parziale | Il requisito che l'installer non tocchi mai l'SSH dell'host e apra la 2222 (M-08) non ha test: lo copre l'e2e di installazione. |
| R8 | Git LFS dopo la v1 (esclusione); nulla in M-03 deve impedirlo | | n/a, motivata | Nessun test lo verifica: esclusione di prodotto, non una funzione. Nota: sshd rifiuta oggi `git-lfs-authenticate` (`services/git/internal/sshd/server_test.go`, TestRifiuti), coerente con «LFS dopo la v1» ma da rivedere quando LFS arriva. |
| R9 | Protezione minima del branch principale: no force-push né eliminazione, attiva di default, disattivabile da `admin` | `services/core/internal/stackitest/git_integration_test.go#TestGitClientReale/R9_branch_principale_protetto`<br>`services/git/internal/smarthttp/receive_test.go#TestBranchPrincipaleProtetto`<br>`services/git/internal/smarthttp/receive_test.go#TestBranchPrincipaleSenzaProtezione`<br>`services/git/internal/sshd/receive_test.go#TestBranchPrincipaleProtettoViaSSH`<br>`services/git/internal/receiverules/receiverules_test.go#TestProtectedBranch`<br>`services/core/internal/httpserver/repos_integration_test.go#TestRepos_Impostazioni/protezione_attiva_di_default_e_disattivabile` | coperta | |
| R10 | Archiviazione: leggibile e clonabile, rifiuta push e impostazioni, etichetta Archived, issues di sola lettura | `services/core/internal/stackitest/git_integration_test.go#TestGitClientReale/R10_repo_archiviato`<br>`services/git/internal/smarthttp/smarthttp_test.go#TestRepoArchiviatoSiLeggeMaNonSiScrive`<br>`services/git/internal/sshd/server_test.go#TestRepoArchiviatoRifiutaPush`<br>`services/core/internal/httpserver/repos_integration_test.go#TestRepos_Impostazioni/archiviazione_e_riattivazione`<br>`web/src/pages/repos/RepoSettings.test.tsx#mostra Archived e disabilita tutto tranne Unarchive` | parziale | Issues di sola lettura sul repo archiviato: manca (M-05, per GIT-113). |
| R11 | Nomi dei repo: minuscole, cifre, `-` `_` `.`, 1–100 caratteri, non inizia con `.`, non finisce con `.git`, unico per owner | `services/core/internal/httpserver/repos_integration_test.go#TestRepos_Creazione/nome_non_valido_400`<br>`services/core/internal/httpserver/repos_integration_test.go#TestRepos_Creazione/nome_duplicato_409`<br>`services/core/internal/store/resource_integration_test.go#TestStore_Create_RepoTypeNonHaUnicitaTypeName`<br>`web/src/pages/repos/Repos.test.tsx#validateRepoName (R11)` | coperta | |
| R12 | Eliminazione e ripristino a chi ha `admin` e all'amministratore dell'installazione; lista «Deleted repositories» con giorni rimasti | `services/core/internal/httpserver/repos_trash_integration_test.go#TestRepos_EliminazioneERipristino/senza_admin_403_o_404`<br>`services/core/internal/httpserver/repos_trash_integration_test.go#TestRepos_EliminazioneERipristino/ripristino_senza_admin`<br>`services/core/internal/httpserver/repos_trash_integration_test.go#TestRepos_EliminazioneERipristino/elenco_eliminati_con_giorni_rimasti`<br>`services/core/internal/httpserver/repos_trash_integration_test.go#TestRepos_EliminazioneERipristino/amministratore_di_sistema`<br>`web/src/pages/repos/RepoSettings.test.tsx#elenca i repo eliminati con i giorni rimasti, per l` | coperta | |

## P — Permessi e visibilità (`identita-e-sicurezza.md`)

| Id | Regola | Test | Stato | Cosa manca |
|---|---|---|---|---|
| P1 | Admin implicito per l'amministratore dell'installazione e per gli owner dell'organizzazione proprietaria | `services/identity/internal/httpapi/owner_visibility_integration_test.go#TestOwnerAndVisibilityRules`<br>`services/identity/internal/permissions/permissions_integration_test.go#TestEffectiveRole`<br>`services/identity/internal/permissions/access_integration_test.go#TestUserAccess_FontiELivelloPiuAlto` | coperta | |
| P2 | Nessun accesso anonimo: senza account non si vede nulla | `services/core/internal/stackitest/security_integration_test.go#TestSecurity/nessuna_credenziale`<br>`services/core/internal/stackitest/git_integration_test.go#TestGitClientReale/senza_credenziali`<br>`services/gateway/internal/httpserver/auth_test.go#TestSicurezza_OgniRottaHaLaSuaDichiarazione` | coperta | |
| P3 | Visibilità privato o interno (lettura a tutti gli utenti con account), nessun livello pubblico; scrittura sempre con `write`/`admin` | `services/identity/internal/httpapi/owner_visibility_integration_test.go#TestOwnerAndVisibilityRules`<br>`services/core/internal/httpserver/repos_integration_test.go#TestRepos_Impostazioni/visibilita_aggiorna_identity`<br>`services/core/internal/httpserver/repos_integration_test.go#TestRepos_Impostazioni/corpo_non_valido`<br>`services/core/internal/stackitest/git_integration_test.go#TestGitClientReale/repo_interno_leggibile_non_scrivibile` | coperta | |
| P4 | Agenti: utenti `agent` senza password, con grant solo sulle risorse necessarie; creati solo dall'admin | `services/identity/internal/users/users_integration_test.go#TestCreateValidationAndConflicts`<br>`services/identity/internal/httpapi/httpapi_integration_test.go#TestCreateUser`<br>`services/identity/internal/httpapi/httpapi_integration_test.go#TestDeleteUser`<br>`services/identity/internal/httpapi/user_tokens_integration_test.go#TestAgentTokensManagedByAdmin`<br>`services/identity/internal/permissions/permissions_integration_test.go#TestGrantCRUD` | coperta | |
| P5 | L'admin gestisce i token degli agenti senza login al loro posto; agente disattivato o eliminato non passa più | `services/identity/internal/httpapi/user_tokens_integration_test.go#TestAgentTokensManagedByAdmin`<br>`services/identity/internal/httpapi/user_tokens_integration_test.go#TestAgentDeactivatedOrDeletedLosesTokens` | coperta | |
| P6 | Repo personali ammessi: admin al proprietario | `services/identity/internal/httpapi/owner_visibility_integration_test.go#TestOwnerAndVisibilityRules`<br>`services/core/internal/httpserver/repos_integration_test.go#TestRepos_Creazione/visibilita_private_di_default_e_contatore` | coperta | |
| P7 | Default privato alla creazione, anche via API o `gs` | `services/core/internal/httpserver/repos_integration_test.go#TestRepos_Creazione/visibilita_private_di_default_e_contatore`<br>`web/src/pages/repos/Repos.test.tsx#crea con i default: private, readme false, nessun modello` | parziale | Manca il default privato via `gs repo create` (M-06, G4). |

## B — Browser del codice (`browser-codice.md`; GIT-89 completa)

| Id | Regola | Test | Stato | Cosa manca |
|---|---|---|---|---|
| B1 | Limiti di visualizzazione (1 MB evidenziato, 5 MB testo semplice, oltre solo Download); immagini; SVG solo come immagine; stessi limiti in API | `services/git/internal/gitread/code_test.go#TestFile_B1`<br>`web/src/pages/repos/FileView.test.tsx#testo fra 1 e 5 MB: semplice, senza evidenziazione`<br>`web/src/pages/repos/FileView.test.tsx#testo oltre 5 MB: messaggio e Download, niente righe`<br>`web/src/pages/repos/FileView.test.tsx#SVG: mostrato solo come <img>, il markup non entra nella pagina`<br>`web/src/pages/repos/FileView.test.tsx#binario: dimensione e Download`<br>`services/core/internal/stackitest/browser_e2e_integration_test.go#TestBrowserCodice/file_B1_tre_fasce` | coperta | |
| B2 | Markdown in stile GitHub, HTML sicuro, riferimenti `#n` e `@utente`, link relativi, link esterni senza provenienza; Mermaid dopo la v1 | `web/src/components/Markdown.test.tsx#neutralizza i payload XSS noti`<br>`web/src/components/Markdown.test.tsx#rende tabelle, liste di attività, autolink e ancore sui titoli`<br>`web/src/components/Markdown.test.tsx##n, owner/repo#n e @utente diventano link`<br>`web/src/components/Markdown.test.tsx#risolve link e immagini nel repo e nel ref`<br>`web/src/components/Markdown.test.tsx#link esterni: _blank, noopener noreferrer, no-referrer`<br>`web/src/components/Markdown.test.tsx#mermaid resta un blocco di codice`<br>`services/core/internal/stackitest/browser_e2e_integration_test.go#TestBrowserCodice/readme`<br>`services/core/internal/stackitest/browser_e2e_integration_test.go#TestBrowserCodice/ui_smoke` | coperta | |
| B3 | Raw e archivi solo con sessione o token `read`; raw mai come pagina eseguibile; niente link anonimi | `services/core/internal/stackitest/code_reads_integration_test.go#TestCodeReads/raw_header_di_sicurezza`<br>`services/core/internal/httpserver/code_reads_integration_test.go#TestCodeReads_Permessi`<br>`services/git/internal/gitread/code_test.go#TestRaw_SafeTypes`<br>`services/git/internal/gitread/code_test.go#TestArchives_SameTree`<br>`services/core/internal/stackitest/browser_e2e_integration_test.go#TestBrowserCodice/raw_header_di_sicurezza`<br>`services/core/internal/stackitest/browser_e2e_integration_test.go#TestBrowserCodice/archivi`<br>`services/core/internal/stackitest/browser_e2e_integration_test.go#TestBrowserCodice/permessi` | coperta | |
| B4 | Storico per file e blame riga per riga (autore con badge «agent», fino a 1 MB); anche via API e `gs` | `services/git/internal/gitread/gitread_test.go#TestBlameMultipleAuthors`<br>`services/git/internal/httpserver/reads_test.go#TestReads_BlameTooLarge`<br>`services/core/internal/stackitest/code_reads_integration_test.go#TestCodeReads/autori_collegati_agli_utenti`<br>`web/src/pages/repos/FileView.test.tsx#mostra commit, autore con badge agent e data per ogni blocco`<br>`web/src/pages/repos/FileView.test.tsx#oltre 1 MB non chiama il blame`<br>`services/core/internal/stackitest/browser_e2e_integration_test.go#TestBrowserCodice/storico`<br>`services/core/internal/stackitest/browser_e2e_integration_test.go#TestBrowserCodice/blame` | parziale | Manca `gs` (M-06): nessun comando né test per storico e blame da CLI. |
| B5 | «Go to file» approssimato; «Search code» nel repo, massimo 100 risultati, senza indice e con tempo massimo; anche via API e `gs` | `services/git/internal/gitread/search_test.go#TestFiles`<br>`services/git/internal/gitread/search_test.go#TestSearchCode_LimitReached`<br>`services/git/internal/gitread/search_test.go#TestSearchCode_TimeoutInjected`<br>`services/core/internal/httpserver/code_reads_integration_test.go#TestCodeReads_Permessi/files_e_search_instradamento`<br>`web/src/pages/repos/CodeBrowser.test.tsx#Go to file: corrispondenza approssimata`<br>`web/src/pages/repos/CodeBrowser.test.tsx#Search code porta ai risultati con query e ref`<br>`services/core/internal/stackitest/browser_e2e_integration_test.go#TestBrowserCodice/ricerca_e_elenco_file`<br>`services/core/internal/stackitest/browser_e2e_integration_test.go#TestBrowserCodice/ui_smoke`<br>`web/src/pages/repos/TagsSearch.test.tsx#elenca percorso, riga e frammento evidenziato con link alla riga della vista file`<br>`web/src/pages/repos/TagsSearch.test.tsx#avvisa quando i risultati sono limitati a 100`<br>`web/src/smoke/codeBrowser.smoke.test.tsx#risultati di Search code` | parziale | Manca `gs` (M-06). La pagina dei risultati è di GIT-94 (su main) e il suo smoke sullo stack vero è in `ui_smoke`. |
| B6 | Diff con limiti progressivi (500 righe chiuse, file generati e di lock chiusi, oltre 300 file o 20.000 righe solo elenco e `.diff`/`.patch`), unificata o affiancata, «ignora spazi» | `services/git/internal/gitread/gitread_test.go#TestCommitModifiedTagsAndLock`<br>`services/git/internal/gitread/gitread_test.go#TestCommitRenameAndLarge`<br>`services/git/internal/gitread/gitread_test.go#TestCommitListOnlyOverFileLimit`<br>`services/git/internal/gitread/gitread_test.go#TestCommitListOnlyOverLineLimit`<br>`services/git/internal/gitread/gitread_test.go#TestCommitWhitespace`<br>`services/git/internal/gitread/gitread_test.go#TestDownloadDiffAndPatchApply`<br>`services/core/internal/stackitest/browser_e2e_integration_test.go#TestBrowserCodice/dettaglio_commit_B6`<br>`services/core/internal/stackitest/browser_e2e_integration_test.go#TestBrowserCodice/diff_e_patch_scaricabili`<br>`web/src/pages/repos/Commits.test.tsx#file chiuso con motivo e Load diff`<br>`web/src/pages/repos/Commits.test.tsx#Unified e Split`<br>`web/src/pages/repos/Commits.test.tsx#ignora spazi: ricarica con ignoreWhitespace`<br>`web/src/pages/repos/Commits.test.tsx#oltre i limiti: solo elenco, avviso e download` | coperta |  |
| B7 | Solo tag nella v1: pagina Tags con data, commit e messaggio dei tag annotati, download; release in v2 | `services/git/internal/gitread/code_test.go#TestBranchesAndTags`<br>`services/core/internal/stackitest/code_reads_integration_test.go#TestCodeReads/tag_con_indirizzi_di_download`<br>`services/core/internal/httpserver/code_reads_integration_test.go#TestCodeReads_Permessi/tag_con_indirizzi_e_protezione_branch`<br>`services/core/internal/stackitest/browser_e2e_integration_test.go#TestBrowserCodice/branch_e_tag`<br>`services/core/internal/stackitest/browser_e2e_integration_test.go#TestBrowserCodice/ui_smoke`<br>`web/src/pages/repos/TagsSearch.test.tsx#offre il download ZIP e tar.gz di ogni tag`<br>`web/src/pages/repos/TagsSearch.test.tsx#mostra il tag annotato col messaggio e quello leggero senza`<br>`web/src/smoke/codeBrowser.smoke.test.tsx#23 tag` | coperta | |

## I — Issues (`issues.md`; li riempie GIT-113)

| Id | Regola | Test | Stato | Cosa manca |
|---|---|---|---|---|
| I1 | Numerazione condivisa con le PR | | da compilare | |
| I2 | Due stati, chiusura con motivo | | da compilare | |
| I3 | `read` apre e commenta, `write` gestisce | | da compilare | |
| I4 | Modifiche tracciate, issues non eliminabili | | da compilare | |
| I5 | Etichette predefinite | | da compilare | |
| I6 | Fino a 10 assegnatari con `write` | | da compilare | |
| I7 | Milestone per repo | | da compilare | |
| I8 | Menzioni rispettose della visibilità | | da compilare | |
| I9 | Allegati protetti | | da compilare | |
| I10 | Ricerca con sintassi GitHub | | da compilare | |
| I11 | Blocco e modelli sì, trasferimento no | `services/core/internal/httpserver/issue_templates_integration_test.go#TestIssueTemplates_SenzaCartella`<br>`services/core/internal/httpserver/issue_templates_integration_test.go#TestIssueTemplates_UnModello`<br>`services/core/internal/httpserver/issue_templates_integration_test.go#TestIssueTemplates_PiuModelliOrdinati`<br>`services/core/internal/httpserver/issue_templates_integration_test.go#TestIssueTemplates_ModelloMalformatoSaltato`<br>`services/core/internal/httpserver/issue_templates_integration_test.go#TestIssueTemplates_ModelloSenzaFrontMatter`<br>`services/core/internal/httpserver/issue_templates_integration_test.go#TestIssueTemplates_SoloMarkdownNonSaltati`<br>`services/core/internal/httpserver/issue_templates_integration_test.go#TestIssueTemplates_Permessi` | coperta | |

## C — Collegamenti, notifiche, webhook (`collegamenti-notifiche-webhook.md`)

| Id | Regola | Test | Stato | Cosa manca |
|---|---|---|---|---|
| C1 | Riferimenti tra repo | | da compilare | |
| C2 | Chiusura via commit | | da compilare | |
| C3 | Chi segue cosa | | da compilare | |
| C4 | Agenti: stessa casella delle persone | | da compilare | |
| C5 | Email | | da compilare | |
| C6 | Webhook | | da compilare | |
| C7 | Consegna | | da compilare | |
| C8 | Protezione SSRF | | da compilare | |
| C9 | Conservazione notifiche | | da compilare | |

## G — CLI `gs` e skills (`cli-gs-skills.md`)

| Id | Regola | Test | Stato | Cosa manca |
|---|---|---|---|---|
| G1 | Familiare come `gh`, non un clone | | da compilare | |
| G2 | Token | | da compilare | |
| G3 | Output | | da compilare | |
| G4 | Comandi v1 | | da compilare | |
| G5 | Skills Agent Skills | | da compilare | |
| G6 | Distribuzione | | da compilare | |
| G7 | Più istanze | | da compilare | |
| G8 | Operazioni distruttive | | da compilare | |

## N — Installazione, requisiti (`installazione-e-deploy.md`)

| Id | Regola | Test | Stato | Cosa manca |
|---|---|---|---|---|
| N1 | Hardware | | da compilare | |
| N2 | Sistemi | | da compilare | |
| N3 | Nodi | | da compilare | |
| N4 | Rete | | da compilare | |
| N5 | TLS: HTTPS di default con CA interna, Let's Encrypt o certificato del cliente; `--insecure-http` con avviso | `admin/internal/status/api_test.go#TestAPI_HTTPSConCAInterna`<br>`admin/internal/status/api_test.go#TestAPI_InsecureResta_HTTP`<br>`web/src/components/InsecureHttpBanner.test.tsx#http su un host di rete` | parziale | Installazione, CA, redirect 80→443, `--tls-cert` e `--insecure-http` si provano con `deploy/test-vm/e2e.ps1` sulla VM (lanciato dal board) e a mano su `deploy/gitstack-tls.sh`, non in CI; Let's Encrypt non ha una prova reale. |
| N6 | Nome host | | da compilare | |

## W — Windows via WSL2 di prova (`installazione-e-deploy.md`)

| Id | Regola | Test | Stato | Cosa manca |
|---|---|---|---|---|
| W1 | Windows via WSL2 di prova (script PowerShell, avvio al login) | | da compilare | |
| W2 | Windows via WSL2 di prova (accesso locale, rete opzionale) | | da compilare | |

## V — Rilascio v1.0 (`rilascio-v1.md`)

| Id | Regola | Test | Stato | Cosa manca |
|---|---|---|---|---|
| V1 | Criteri di uscita, tutti obbligatori | | da compilare | |
| V2 | Sicurezza, revisione interna | | da compilare | |
| V3 | Carico, solo test di fumo | | da compilare | |
| V4 | Versioni e supporto | | da compilare | |
| V5 | Catena di fornitura | | da compilare | |
| V6 | Documentazione | | da compilare | |
| V7 | Dogfooding presto | | da compilare | |
| V8 | Pubblicazione | | da compilare | |
