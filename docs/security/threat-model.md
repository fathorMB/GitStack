# GitStack threat model (internal review)

Release rule V2 (M-09, GIT-219): threat model of the risk areas of GitStack v1.0, written together with the OWASP ASVS 4.0.3 level 2 checklist in [asvs-l2.md](asvs-l2.md). Internal review by reading code, chart and tests; no penetration test, nothing exploited at run time.

How to read it:

- Every mitigation cites the file or the test that proves it, as `path#TestName`. All references were checked against the tree when this file was written. Many integration tests only run with `-tags=integration` and Postgres (see `docs/rules-coverage.md`): the reference proves that the test exists, not that it passed.
- `F<n>` is a finding of the register in [asvs-l2.md](asvs-l2.md#findings-register) (severity and correction test there). A residual risk without an `F` is accepted by design and written here so that it is a decision, not an omission.
- The areas are the ones listed by rule V2: permissions (R and I), SSRF of webhooks, mirror in push, mirror in pull (`pkg/egress`), Markdown, raw and SVG, agent tokens (scopes, GIT-190) and `gs`.

## 1. System, assets and trust boundaries

```
 browser / gs / git client                       outside world (webhook targets, mirror origins,
        |  HTTPS (Traefik, TLS)                   OIDC provider, SMTP)  <---- egress (pkg/egress, git)
        v                                                         ^
 [ Traefik ingress ]--/ , /api, /raw --> [ web (nginx, SPA) ]     |
        |                                    |  /api, raw          |
        |                                    v                     |
        |                              [ gateway ] -- signed identity header --> [ core ] --+
        |                                    |  verify credential / permission    |  ^       |
        |                                    v                                    v  |       |
        |                              [ identity ] <------ service secret ---- [ git ] <-----+
        |  git HTTPS and SSH (2222)                                         [ NATS ]  [ Postgres ]
        +--------------------------------------------------------------------> [ git ]
```

| Boundary | Crossing | Authentication of the crossing |
|---|---|---|
| B1 Internet or LAN to Traefik | Browser, `gs`, git clients | TLS at Traefik (`docs/tls.md`); then session cookie or `gst_` token |
| B2 Traefik to web and gateway | `/api/v1/*`, raw files | Gateway authenticates every route from the contract: `services/gateway/internal/middleware/auth.go` |
| B3 Gateway to core, identity | Identity of the user | HMAC-signed `X-Gitstack-*` headers with timestamp; client copies are stripped: `services/gateway/internal/trust/trust_test.go#TestStripClientHeaders`, `services/core/internal/stackitest/security_integration_test.go#TestSecurity/header_identita_falsificati` |
| B4 Services to identity `/internal/*` | Credential and permission checks | One service secret per caller, constant-time compare, per-caller allow list: `services/identity/internal/httpapi/tokens_keys_integration_test.go#TestEmptyServiceSecretRejectsAll`, `services/identity/router_test.go#TestBuildRouter_SegretiPerChiamante` |
| B5 git clients to the git service | Smart HTTP and SSH, not through the gateway | Token as Basic password or registered SSH key, checked by `services/git/internal/access/access.go` |
| B6 Services to Postgres and NATS | Data and events | Database roles per service; NATS without credentials (F8) |
| B7 Core and git to the outside | Webhooks, mirrors, SMTP, OIDC discovery | `pkg/egress` policy and, for git, a pinned address (sections 3 to 5) |

| Asset | Where | Protection |
|---|---|---|
| Passwords | `identity.credentials` | argon2id: `services/identity/internal/password/password_test.go#TestHashFormatAndVerify` |
| Session values, personal tokens | `identity.sessions`, `identity.api_tokens` | Only SHA-256 stored: `services/identity/internal/apitokens/apitokens_integration_test.go#TestCreateStoresOnlyHash` |
| Webhook secrets, mirror credentials, OIDC client secret | core database, identity OIDC configuration | AES-256-GCM: `services/core/internal/webhooks/unit_test.go#TestKeyring_CifraDecifraERotazione`, `services/identity/internal/oidc/crypto_test.go` |
| Service secret, database password, keys | Kubernetes Secrets | Not encrypted at rest, kubeconfig world-readable (F2); one secret per calling service, rotation in `docs/operations.md` (F9) |
| Repository content | git volume | Role checks in git and core; archived and deleted states: `services/git/internal/smarthttp/smarthttp_test.go#TestRepoArchiviatoSiLeggeMaNonSiScrive` |
| Issues, comments, hidden issues, notifications | `core.*` | Role and hidden-issue rules (section 2) |
| Attachments | core volume | Content-typed, served as download: `services/core/internal/httpserver/attachments_integration_test.go#TestAttachments_UploadEDownload` |
| User names and emails | `identity.users` | Clear in the database (F20) |

Attackers considered: an anonymous network client; an authenticated user with no access to a repository; a user with `read` or `write` on one repository; a repository admin (can create webhooks and mirrors); an agent whose token leaked or whose prompt was injected; a malicious origin of a pull mirror or target of a webhook; a local account on the server host. Out of scope: an attacker with root on the host, and denial of service by an administrator.

## 2. Permissions (R and I)

**Asset.** Visibility and content of repositories (rules R, P) and of issues (rule I, hidden issues I4); the ability to write, to administer and to grant.

**Attacker.** A logged-in user outside the repository or organisation; a user with `read` trying to write; an owner of another organisation; a token with fewer scopes than needed; an anonymous client.

| Threat | Mitigation | Proof | Residual |
|---|---|---|---|
| Access without credentials (P2) | Every route needs a declaration of security from the contract; undeclared routes are 404 | `services/gateway/internal/httpserver/auth_test.go#TestSicurezza_OgniRottaHaLaSuaDichiarazione`, `services/core/internal/stackitest/security_integration_test.go#TestSecurity/nessuna_credenziale` | None known |
| IDOR on a resource of another organisation | Permission checked per `{resourceId}` by identity; denied looks like missing | `services/gateway/internal/httpserver/permissions_test.go#TestPermessi_NegatoUgualeSeLaRisorsaNonEsiste`, `services/gateway/internal/httpserver/permissions_test.go#TestPermessi_OgniRottaDiCoreSuRisorsaDichiaraIlPermesso`, `services/core/internal/stackitest/security_integration_test.go#TestSecurity/risorsa_di_altra_organizzazione` | Core trusts the gateway signature; git cannot sign for core beyond `GET /repos/{owner}/{repo}` (F9 resolved, GIT-235) |
| Role escalation through organisation owners, teams or visibility | Effective role computed in one place; visibility private or internal only | `services/identity/internal/permissions/permissions_integration_test.go#TestEffectiveRole`, `services/identity/internal/httpapi/owner_visibility_integration_test.go#TestOwnerAndVisibilityRules`, `services/identity/internal/httpapi/orgs_integration_test.go#TestOrgNonOwnerForbidden` | None known |
| Disclosure through derived views (search, notifications, events, commit links) | Hidden issues and unreadable repos are filtered in SQL | `services/core/internal/httpserver/issues_search_integration_test.go#TestIssuesSearch_VisibilitaEPermessi`, `services/core/internal/httpserver/notifications_integration_test.go#TestNotifiche_IssueNascostaSoloAdminI4`, `services/core/internal/httpserver/issues_commit_links_integration_test.go#TestIssueEvents_CommitDiAltroRepoVisibileSoloAChiVedeEntrambi` | New derived views need the same test |
| Writing with `read`, closing others' issues (I3) | Role checks in handlers | `services/core/internal/httpserver/issues_integration_test.go#TestIssues_StatoConMotivoI2ePermessiI3` | None known |
| Pushing to an archived or protected repo (R9, R10) | Checks in HTTP and SSH paths and a pre-receive hook | `services/git/internal/smarthttp/receive_test.go#TestBranchPrincipaleProtetto`, `services/git/internal/sshd/server_test.go#TestRepoArchiviatoRifiutaPush` | None known |
| Existence leak of private repos | 404 for missing and unreadable alike on git paths | `services/git/internal/access/access_test.go` | Timing not measured |
| Stale decision after revocation | Cache of 30 s for a positive decision, 5 s negative; logout and password change flush the entry | `services/gateway/internal/identityclient/cache_test.go#TestCache_NegativoCinqueSecondi`, `services/gateway/internal/httpserver/logout_scopes_test.go#TestAuth_LogoutSvuotaLaCache` | Up to 30 s on other credentials of the same user: accepted |
| Scope bypass by minting a token | Contract says a token cannot have scopes its creator lacks | No test; the handler does not enforce it | **F1 (high)** |
| Unattributed changes of grants and roles | None | None | F10: no audit trail |

**Residual risk.** F1 and F10. Roles are a hierarchy (read, write, admin) with no per-branch or per-path rules, by design (R9 is the only branch rule).

## 3. SSRF of webhooks (C6, C7, C8)

**Asset.** Services reachable from core: the cluster (core, identity, NATS, Postgres), the node (loopback, kubelet, k3s API), cloud metadata and the LAN.

**Attacker.** A user who can create a webhook (repository admin, organisation owner), or a remote server that answers with redirects or changing DNS records.

| Threat | Mitigation | Proof | Residual |
|---|---|---|---|
| Literal internal address, alternative forms (`::ffff:127.0.0.1`, NAT64, zone ids) | Decision on the normalised IP, not on the text | `pkg/egress/egress_test.go#TestCheckIPCategories` | None known |
| Host name resolving to an internal address; DNS rebinding | Check inside the dialer `Control`, on the address actually connected | `pkg/egress/egress_test.go#TestDNSResolvingToBlockedAddress`, `pkg/egress/egress_test.go#TestControlBlocksAtConnect`, `pkg/egress/egress_test.go#TestRealDialerBlocksLoopback` | With a corporate proxy the proxy resolves the name (documented in `pkg/egress/README.md`) |
| Redirect to an internal address | Every hop is a new connection judged by the dialer; at most 3 hops | `pkg/egress/egress_test.go#TestRedirects`, `services/core/internal/webhooks/unit_test.go#TestNewEgress_TreRedirectAlMassimo` | None known |
| URL with credentials, `localhost`, non-HTTP scheme | Validated at creation | `services/core/internal/webhooks/unit_test.go#TestCheckURL`, `services/core/internal/httpserver/webhooks_integration_test.go#TestWebhooks_ConfigurazioneEValidazione` | `http://` is allowed |
| End to end block | A blocked destination is marked failed without retry and recorded in the delivery log | `services/core/internal/stackitest/m06_integration_test.go#TestM06E2E/destinazioni_ssrf_bloccate` | None known |
| Corporate proxy bypasses policy | Policy applied to the real destination before contacting the proxy | `pkg/egress/proxy_test.go#TestProxy_DestinazioneVietataNonContattaIlProxy`, `pkg/egress/proxy_test.go#TestProxy_RedirectVersoDestinazioneInternaRifiutato` | The proxy address is trusted |
| Forged or leaked signature secret | HMAC-SHA256 signature, secret encrypted at rest, never returned | `services/core/internal/webhooks/unit_test.go#TestSign_EsempioDellaDocumentazione`, `services/core/internal/httpserver/webhooks_integration_test.go#TestWebhooks_SenzaSegretoEventiSelezionatiENascoste` | Rotation documented in `docs/operations.md` ("Key lifecycle and rotation", F9 resolved, GIT-235) |
| Reading internal answers through the log | Answer clipped to 4 KB | `services/core/internal/httpserver/webhooks_integration_test.go#TestWebhooks_RispostaTroncataA4KB` | The excerpt is visible to the repository admin |
| Webhook for a repo the user cannot administer | Role check | `services/core/internal/httpserver/webhooks_integration_test.go#TestWebhooks_Permessi` | None known |

**Residual risk.** F7 (resolved, GIT-233): the node's own addresses and the Kubernetes ports (6443, 10250, 10255, 2379-2380) on private addresses are always blocked (`pkg/egress/egress_test.go#TestNodeAddressesAreBlockedByDefault`). Other RFC 1918 hosts remain reachable by default, because CI servers live on the LAN; the administrator closes the list with `egress.allow` (`install.sh --egress-allow`).

## 4. Mirror in push (V8-mirror, GIT-179)

**Asset.** Integrity of the destination repository, the destination token, the internal network, and the confidentiality of the source repository.

**Attacker.** A repository admin (configures URL and token); a destination server that is malicious or compromised.

| Threat | Mitigation | Proof | Residual |
|---|---|---|---|
| Destination in the internal network | Core resolves the name, judges every address, git gets one pinned address (`http.curloptResolve`), redirects off, `protocol.allow=never` | `services/core/internal/mirrors/mirrors_test.go#TestEgress_PolicyDiDefault`, `services/core/internal/mirrors/mirrors_test.go#TestParseURL`, `services/git/internal/mirrorpush/mirrorpush.go#pushArgs` | F7 (LAN ranges allowed) |
| Token in argv, logs, API or errors | Token travels in the environment as an HTTP header; stderr redacted; API never returns it | `services/git/internal/mirrorpush/mirrorpush_test.go#TestArgvSenzaToken`, `services/git/internal/mirrorpush/mirrorpush_test.go#TestPush_ErroreDelServerNonPerdeIlToken`, `services/git/internal/httpserver/mirror_test.go#TestMirrorPush_IlTokenNonEsceMai`, `services/core/internal/httpserver/mirrors_integration_test.go#TestMirrors_API_TokenCifratoEMaiRestituito` | Encrypted with a key kept in a Kubernetes Secret (F2) |
| Overwriting or deleting history at the destination | Never `--force`, never `+`, never delete; a diverged destination stops and alerts | `services/git/internal/mirrorpush/mirrorpush_test.go#TestPush_DestinazioneDivergente_NienteForce`, `services/core/internal/httpserver/mirrors_integration_test.go#TestMirrors_Divergenza_NotificaERipartenza` | None known |
| Resource exhaustion | One push per mirror, timeout, backoff | `services/git/internal/mirrorpush/mirrorpush_test.go#TestPush_UnSoloPushPerMirror`, `services/git/internal/mirrorpush/mirrorpush_test.go#TestPush_TimeoutEBloccoDelDNS`, `services/core/internal/httpserver/mirrors_integration_test.go#TestMirrors_Backoff` | None known |
| Only admins configure | Role check | `services/core/internal/httpserver/mirrors_integration_test.go#TestMirrors_API_ValidazioneEEgress` | An admin of a private repository can publish it outside by design |

**Residual risk.** By design, a repository admin can copy the main branch and the tags of a private repository to any HTTPS server allowed by egress; this is the point of the feature, and the mirror is listed in the repository settings.

## 5. Mirror in pull (MP1 to MP8, GIT-201)

**Asset.** The git volume and the local repository, the credentials of the origin, the internal network.

**Attacker.** The origin (malicious, compromised or redirecting) and the admin who configures it.

| Threat | Mitigation | Proof | Residual |
|---|---|---|---|
| Origin in the internal network or redirect | Same pinned-address policy as push; `https://` only, no credentials in URL | `services/git/internal/mirrorpull/mirrorpull_test.go#TestPull_DestinazioniVietateSenzaConnessione`, `services/git/internal/mirrorpull/mirrorpull_test.go#TestPull_Validate`, `services/core/internal/httpserver/mirrors_pull_engine_integration_test.go#TestPullMirror_API_ValidazioneEEgress` | F7 |
| Origin rewrites history to destroy local data | Fetch into a temporary namespace without force or prune; rewritten refs are marked `diverged` | `services/git/internal/mirrorpull/mirrorpull_test.go#TestPull_DivergenzaNienteSovrascrittura`, `services/git/internal/mirrorpull/mirrorpull_test.go#TestPull_OrigineIndietroNonRiavvolge`, `services/git/internal/mirrorpull/mirrorpull_test.go#TestPull_ArgvSenzaCredenzialiNeForce` | None known |
| Malicious content executed on the server | Bare repository, no checkout, no hooks transferred, submodules not fetched (`--no-recurse-submodules`) | `services/git/internal/mirrorpull/mirrorpull.go#fetchArgsProxy` | Objects are not fsck-checked (F18) |
| Disk exhaustion by a huge origin | Timeout and one fetch at a time only | `services/git/internal/mirrorpull/mirrorpull_test.go#TestPull_Timeout`, `services/git/internal/mirrorpull/mirrorpull_test.go#TestPull_UnSoloFetchPerRepo` | No size cap (F18) |
| Credential leak | Encrypted, never returned, not in argv or logs; deleted at stop | `services/git/internal/httpserver/mirrorpull_test.go#TestMirrorPull_IlTokenNonEsceMai`, `services/git/internal/mirrorpull/mirrorpull_test.go#TestPull_ErroreDelServerNonPerdeLeCredenziali`, `services/core/internal/httpserver/mirrors_pull_engine_integration_test.go#TestPullMirror_PausaRipresaInterruzione` | F2 |
| Writing to a mirrored repo | Read-only over HTTPS, SSH and `gs` | `services/git/internal/smarthttp/mirror_readonly_test.go#TestMirrorInPullRifiutaIlPush`, `services/git/internal/sshd/mirror_readonly_test.go#TestMirrorInPullRifiutaIlPushViaSSH` | None known |
| Mirror used to push to the origin | Not possible | `services/core/internal/httpserver/mirrors_pull_engine_integration_test.go#TestPullMirror_DivietoMirrorInPushVersoLOrigine` | None known |

**Residual risk.** F18 (no fsck, no size cap) and the fact that mirrored content is untrusted input for every other area (Markdown and raw are rendered as for any repository, section 6).

## 6. Markdown, raw and SVG (B1 to B3, B5)

**Asset.** The session of every reader (same origin as the API) and the confidentiality of private files.

**Attacker.** Anyone who can write Markdown (issue, comment, README via push or mirror) or add a file to a repository or an issue.

| Threat | Mitigation | Proof | Residual |
|---|---|---|---|
| Script, event handlers, iframes, forms, `javascript:` and `data:` links in Markdown | `rehype-raw` then `rehype-sanitize` (allow list in the GitHub style); element ids prefixed to stop DOM clobbering | `web/src/components/Markdown.test.tsx#neutralizza i payload XSS noti`, `web/src/components/Markdown.test.tsx#ammette details, summary, sub, sup, kbd, br, img; rimuove script, iframe, style, on*`, `web/src/components/Markdown.test.tsx#gli id dei titoli hanno il prefisso e non collidono con la pagina` | No CSP as second layer (F3) |
| Reverse tabnabbing and referrer leak | External links `noopener noreferrer`, images `no-referrer` | `web/src/components/Markdown.test.tsx#link esterni: _blank, noopener noreferrer, no-referrer` | Remote images can be used as a tracking pixel: accepted |
| Raw file executed as a page | Text is `text/plain` inline; everything else, SVG included, is `application/octet-stream` plus `attachment`; always `nosniff` and `Content-Security-Policy: sandbox` | `services/git/internal/gitread/code_test.go#TestRaw_SafeTypes`, `services/core/internal/stackitest/code_reads_integration_test.go#TestCodeReads/raw_header_di_sicurezza`, `services/core/internal/stackitest/browser_e2e_integration_test.go#TestBrowserCodice/raw_header_di_sicurezza` | None known |
| Header injection through file names | Sanitised file name | `services/git/internal/gitread/code_test.go#TestRaw_SafeTypes` | None known |
| SVG as active markup | File view uses `<img>` only; uploads reject HTML, XML and SVG even as text | `web/src/pages/repos/FileView.test.tsx#SVG: mostrato solo come <img>, il markup non entra nella pagina`, `services/core/internal/attachments/attachments_test.go#TestDetect` | None known |
| Attachment from another repository or a hidden issue | Per-repo storage, uploader or admin only until linked | `services/core/internal/httpserver/attachments_integration_test.go#TestAttachments_IssueNascosta` | None known |
| Raw and archives without login | Session or `read` token required | `services/core/internal/httpserver/code_reads_integration_test.go#TestCodeReads_Permessi` | Cached by the browser (F4) |
| Search used for ReDoS or option injection | Literal search (`-F`), query as `-e`, result and time limits | `services/git/internal/gitread/search_test.go#TestSearchCode_LiteralText`, `services/git/internal/gitread/search_test.go#TestSearchCode_TimeoutInjected` | None known |

**Residual risk.** The sanitiser is the only layer between a Markdown author and the reader's session (F3). Raw and attachments share the origin of the SPA; the `sandbox` CSP and the content type make them inert, but a regression there is exploitable, which is why the headers are tested end to end.

## 7. Agent tokens (scopes, GIT-190) and `gs`

**Asset.** The ability to act as a user or an agent: read and write repositories, administer organisations, manage tokens.

**Attacker.** Anyone who obtains a token (CI log, environment, prompt injection of an agent), another user, a malicious host that `gs` is pointed at.

| Threat | Mitigation | Proof | Residual |
|---|---|---|---|
| Guessing or database leak of tokens | 256 random bits, only the hash is stored, 54-character format with checksum | `services/identity/internal/tokens/tokens_test.go#TestGenerate`, `services/identity/internal/apitokens/apitokens_integration_test.go#TestCreateStoresOnlyHash` | None known |
| Long-lived token | Expiry required, at most 365 days by default, last use recorded, revocable | `services/identity/internal/apitokens/apitokens_integration_test.go#TestCreateValidation`, `services/identity/internal/apitokens/apitokens_integration_test.go#TestRevokeAndList` | No expiry reminder (F13) |
| Use beyond the granted scope | Scope per route in the gateway and per operation in git; `write` implies `read` through one rule | `pkg/scopes/scopes_test.go#TestGrants`, `services/gateway/internal/httpserver/auth_test.go#TestAuth_InsufficientScopeDichiaraGliScopeRichiesti`, `services/git/internal/access/access_test.go#TestAuthorizeImplicazioniDegliScope`, `services/git/internal/smarthttp/smarthttp_test.go#TestTokenSoloScritturaClonaFetchaEPusha` | Scope escalation by minting a token (**F1**) |
| Agent token managed by the agent itself | Only an admin creates and revokes agent tokens; deactivated agents lose them | `services/identity/internal/httpapi/user_tokens_integration_test.go#TestAgentTokensManagedByAdmin`, `services/identity/internal/httpapi/user_tokens_integration_test.go#TestAgentDeactivatedOrDeletedLosesTokens` | F1 applies to the admin path too |
| Token in the URL or in logs | Header only; request log keeps the path only | `services/gateway/internal/middleware/logging_test.go#TestLogging_UnaRigaJSONConCampiAttesi`, `services/gateway/internal/httpserver/auth_test.go#TestAuth_CredenzialeTroppoLunga` | None known |
| Long or malformed credential | Rejected at 512 characters before identity is called | `services/gateway/internal/httpserver/auth_test.go#TestAuth_CredenzialeTroppoLunga` | None known |
| SSH key as an unscoped credential | An SSH key is the whole user, by design (`sshScopes` in `services/git/internal/sshd/server.go`) | `services/git/internal/sshd/server_test.go` | Cannot be limited to read-only |
| `gs` stores the token | System keyring, otherwise a user-only file; `GS_TOKEN` is used only for `GS_HOST` | `cli/internal/config/config_test.go#TestPermessiSoloUtente`, `cli/internal/cmd/auth/auth_test.go#TestLoginWithTokenPortachiavi`, `cli/internal/cmd/root/origin_test.go#TestGSTokenSoloVersoGSHost`, `cli/internal/cmd/root/origin_test.go#TestCredentialHelperNonDaGSTokenAdAltriHost` | A process of the same user reads it |
| Token typed on a command line | Read from stdin or the environment | `cli/internal/cmd/auth/auth_test.go#TestLoginSenzaTerminaleRichiedeWithToken` | None known |
| Tampered `gs` binary or install script | Binary served by the instance with SHA-256 sums; releases are signed and verified by `install.sh` and `gitstack upgrade` | `deploy/tests/install_gs_test.sh`, `admin/internal/upgrade/verify_test.go#TestV5RifiutaBinarioAlterato` | The instance serves both file and checksum: trust is the instance (G6) |

**Residual risk.** F1 (high), F10 (no event when a token is created or used from a new place), F13 (no notification), F12 and F16 for web sessions.

## 8. Cross-cutting residual risks

| Topic | Finding |
|---|---|
| Local access to the server gives the whole cluster | F2 |
| Internal network, NATS and database transport | F8 |
| One service secret, no rotation procedure | F9 (resolved, GIT-235) |
| No audit, abuse detection or rate limiting | F10, F6 |
| Browser hardening headers and cache | F3, F4 |
| Body size limits | F5 |
| Second factor | F11 |

## 9. Maintaining this document

Update it in the same item as any change that adds an outbound call, a credential type, a way to render user content, a new route family or a new boundary (V1.1.2 of the checklist). A new finding goes to the register in [asvs-l2.md](asvs-l2.md) with the test that would prove it; the correction item adds the test and moves the row to ok.
