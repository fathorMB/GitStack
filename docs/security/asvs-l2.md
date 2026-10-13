# OWASP ASVS 4.0.3 level 2 checklist (internal review)

Release rule V2 (M-09, GIT-219). Internal security review, no external penetration test.
Reviewed commit: `main` at the time of item GIT-219 (2026-10-12). Companion document: [threat-model.md](threat-model.md).

## How to read this

- The list is the set of requirements marked level 2 in the official ASVS 4.0.3 CSV (`OWASP Application Security Verification Standard 4.0.3-en.csv`, tag `v4.0.3` of github.com/OWASP/ASVS): **259 requirements**, which include all level 1 ones. Level 3-only requirements are out of scope. The titles below are short paraphrases; the normative text is in the OWASP document (CC BY-SA 4.0).
- Outcome: **ok** (the control exists; evidence is a test or a source file), **n/a** (the requirement does not apply; the reason is given) or **problem** (the control is missing or incomplete; the problem id `F<n>` points to the register at the end).
- Evidence is written as `path#TestName` (Go: a `func TestName(`; web: the title of the test; shell and PowerShell tests: the case name). Every reference was checked against the tree when this file was produced. Many integration tests only run with `-tags=integration` and Postgres (see `docs/rules-coverage.md`): the check proves they exist, not that they passed.
- The review is by reading the code, the chart and the tests plus targeted greps; nothing was exploited at run time. A problem found by reading is stated as such in the register.
- Nothing was fixed by this review. Each problem becomes a correction item with its own test (rule V2).

## Summary

| Outcome | Requirements |
|---|---|
| ok | 151 |
| n/a | 52 |
| problem | 56 |
| **total** | **259** |

Problems by severity: critical 0, high 1, medium 8, low 9. No critical problem was found. F1 (high) was resolved by GIT-227, F7 (medium) by GIT-233 and F6 (medium) by GIT-232.

| Chapter | ok | n/a | problem |
|---|---|---|---|
| V1 Architecture, Design and Threat Modeling | 28 | 0 | 10 |
| V2 Authentication | 20 | 24 | 9 |
| V3 Session Management | 14 | 1 | 3 |
| V4 Access Control | 6 | 1 | 2 |
| V5 Validation, Sanitization and Encoding | 21 | 9 | 0 |
| V6 Stored Cryptography | 8 | 2 | 3 |
| V7 Error Handling and Logging | 6 | 0 | 6 |
| V8 Data Protection | 11 | 3 | 1 |
| V9 Communication | 2 | 1 | 4 |
| V10 Malicious Code | 3 | 2 | 0 |
| V11 Business Logic | 5 | 1 | 2 |
| V12 Files and Resources | 10 | 1 | 4 |
| V13 API and Web Service | 6 | 4 | 3 |
| V14 Configuration | 11 | 3 | 9 |

## V1 Architecture, Design and Threat Modeling

| Id | Requirement | Outcome | Evidence or reason |
|---|---|---|---|
| V1.1.1 | Secure SDLC | ok | Every change is a tracked work item with review by the CTO and gated by CI (build, tests, lint, scans): `.github/workflows/ci.yml`, `.github/workflows/security.yml`. |
| V1.1.2 | Threat modeling per design change | **problem** | The first threat model is `docs/security/threat-model.md`, but nothing makes it part of each design change. [F19] |
| V1.1.3 | Security constraints in user stories | ok | Security constraints are product rules with tests (permission rules P1-P7, B3, C8, MP5 in `docs/rules-coverage.md`). |
| V1.1.4 | Trust boundaries and data flows documented | ok | `docs/security/threat-model.md` (section Trust boundaries) and the signed-identity scheme in `services/gateway/internal/trust/trust.go`. |
| V1.1.5 | Architecture and remote services analysed | ok | Architecture in `.prisma/knowledge/topics/architettura.md`; analysis in `docs/security/threat-model.md`. |
| V1.1.6 | Centralised, reusable security controls | ok | One auth/permission middleware generated from the contract (`services/gateway/internal/middleware/auth.go`, `services/gateway/internal/security/gen/main.go`), one scope rule (`pkg/scopes/scopes_test.go#TestGrants`), one egress policy (`pkg/egress/egress_test.go#TestCheckIPCategories`). |
| V1.1.7 | Secure coding checklist available | ok | This checklist (`docs/security/asvs-l2.md`), `docs/security-scans.md` and the per-service READMEs. |
| V1.2.1 | Low-privilege OS accounts per component | ok | Images run as non-root (`USER nonroot` in `services/gateway/Dockerfile`, `services/core/Dockerfile`, `services/identity/Dockerfile`; `USER gitsvc` in `services/git/Dockerfile`). |
| V1.2.2 | Component-to-component calls authenticated | **problem** | HTTP calls are authenticated (`services/core/internal/trust/trust_test.go#TestVerify_Rifiuti`, `services/identity/internal/httpapi/tokens_keys_integration_test.go#TestEmptyServiceSecretRejectsAll`), but NATS has no authentication and nothing isolates pods from each other. [F8] |
| V1.2.3 | Single vetted authentication mechanism with logging | **problem** | Identity is the single mechanism (`services/identity/internal/auth/auth.go`), but authentication events are not logged, so account abuse cannot be detected. [F10] |
| V1.2.4 | Consistent strength across authentication paths | ok | Password, OIDC, personal token and SSH key all resolve through identity: `services/identity/internal/httpapi/tokens_keys_integration_test.go#TestVerifySessionAndServiceAuth`, `services/identity/internal/oidc/service_test.go#TestFinish_TokenValidation`. |
| V1.4.1 | Access control on trusted enforcement points | ok | Enforced in gateway and services, never in the SPA: `services/core/internal/stackitest/security_integration_test.go#TestSecurity`. |
| V1.4.4 | Single access control mechanism | ok | All API routes pass `services/gateway/internal/middleware/auth.go`: `services/gateway/internal/httpserver/auth_test.go#TestSicurezza_OgniRottaHaLaSuaDichiarazione`. Git over HTTPS/SSH has its own check in `services/git/internal/access/access.go` that reuses `pkg/scopes`. |
| V1.4.5 | Permission checked per feature/data item | ok | Resource-level role check by identity: `services/identity/internal/permissions/permissions_integration_test.go#TestEffectiveRole`, `services/gateway/internal/httpserver/permissions_test.go#TestPermessi_OgniRottaDiCoreSuRisorsaDichiaraIlPermesso`. |
| V1.5.1 | Input and output requirements defined | ok | Typed contract with lengths and enums: `api/openapi.yaml`, checked by `.github/workflows/api-contract.yml`. |
| V1.5.2 | No serialization of untrusted objects | ok | Only JSON; no gob/XML/pickle imports in the Go code (`git grep encoding/gob` is empty); bodies decoded with `DisallowUnknownFields` in `services/core/internal/httpserver/resources.go`. |
| V1.5.3 | Input validation on a trusted layer | ok | Server-side: `pkg/names/names_test.go#TestValidateRepoName`, `services/git/internal/gitref/gitref_test.go#TestValidateRef`, `services/git/internal/gitref/gitref_test.go#TestValidatePath`. |
| V1.5.4 | Output encoding near the interpreter | ok | React escaping, JSON encoder, sanitised Markdown (`web/src/components/Markdown.test.tsx#neutralizza i payload XSS noti`), sanitised `Content-Disposition` (`services/git/internal/gitread/code_test.go#TestRaw_SafeTypes`). |
| V1.6.1 | Explicit key management policy | **problem** | Keys can be rotated (`services/core/internal/webhooks/unit_test.go#TestKeyring_CifraDecifraERotazione`) but no document describes key lifecycle, owners and rotation of the service secret, the OIDC key and the keyring. [F9] |
| V1.6.2 | Key material protected by a vault | **problem** | Secrets live in plain Kubernetes Secrets; k3s is installed without `--secrets-encryption` and with a world-readable kubeconfig (`deploy/install.sh`). [F2] |
| V1.6.3 | Keys and passwords replaceable | ok | Webhook keyring has primary and old keys (`services/core/internal/webhooks/unit_test.go#TestKeyring_CifraDecifraERotazione`); service secret and admin secret can come from `existingSecret` (`deploy/gitstack/values.yaml`). |
| V1.6.4 | Client-side secrets not trusted | ok | The browser only holds an HttpOnly session cookie; API tokens are revocable, scoped and expiring (`services/identity/internal/apitokens/apitokens_integration_test.go#TestCreateValidation`). |
| V1.7.1 | Common logging format | ok | `slog` JSON handler in all four services (`services/gateway/main.go`, `services/core/main.go`, `services/identity/main.go`, `services/git/main.go`). |
| V1.7.2 | Logs sent to a remote system | **problem** | Logs go to stdout only; no shipping or alerting is provided or documented. [F10] |
| V1.8.1 | Sensitive data classified | ok | Asset tables in `docs/security/threat-model.md`. |
| V1.8.2 | Protection requirements per class | ok | Passwords argon2id (`services/identity/internal/password/password_test.go#TestHashFormatAndVerify`), tokens hashed (`services/identity/internal/apitokens/apitokens_integration_test.go#TestCreateStoresOnlyHash`), webhook secrets and mirror credentials encrypted (`services/core/internal/httpserver/mirrors_integration_test.go#TestMirrors_API_TokenCifratoEMaiRestituito`). |
| V1.9.1 | Communications between components encrypted | **problem** | Gateway to core/identity/git is plain HTTP, NATS is plain, bundled Postgres uses `sslmode=disable` (`deploy/gitstack/templates/_helpers.tpl`). [F8] |
| V1.9.2 | Authenticity of each side verified | **problem** | No TLS inside the cluster, so no peer certificate is validated; external peers are validated (Go default). [F8] |
| V1.10.1 | Source control with change tickets | ok | Every branch is `item/GIT-n` tied to a work item; PRs run `.github/workflows/ci.yml`. |
| V1.11.1 | Components and their functions documented | ok | `services/gateway/README.md`, `services/identity/README.md`, `services/core/README.md`, `services/git/README.md`. |
| V1.11.2 | Auth/session/access flows without unsynchronised state | ok | Session and token state is in Postgres; password change revokes sessions in the same transaction (`services/identity/internal/auth/auth_integration_test.go#TestChangePasswordRevokesSessions`). The login limiter is per process (documented in `services/identity/internal/loginlimit/loginlimit.go`). |
| V1.12.2 | Uploaded files served as downloads | ok | `services/core/internal/httpserver/attachments_integration_test.go#TestAttachments_UploadEDownload` (octet-stream or fixed type, `attachment`, `nosniff`, `sandbox`). |
| V1.14.1 | Segregation of components of different trust | **problem** | Only web and git are exposed by the ingress, but there is no NetworkPolicy and no pod hardening, so any pod reaches identity, core, NATS and Postgres. [F8] |
| V1.14.2 | Signed binaries and verified deployment | ok | `admin/internal/upgrade/verify_test.go#TestV5RunVerificaTuttoEPassa`, `admin/internal/upgrade/verify_test.go#TestV5RifiutaBinarioAlterato`. |
| V1.14.3 | Build pipeline warns of vulnerable components | ok | `.github/workflows/security.yml` (govulncheck, pnpm audit, gosec, Trivy), gate in `scripts/security/gate.test.mjs`. |
| V1.14.4 | Build step that verifies secure deployment | ok | `.github/workflows/install.yml` installs, upgrades and restores on Ubuntu. |
| V1.14.5 | Deployment sandboxed or isolated | **problem** | Containers are non-root, but no securityContext (read-only root FS, dropped capabilities, seccomp) and no NetworkPolicy in the chart. [F8] |
| V1.14.6 | No deprecated client technologies | ok | React SPA only (`web/package.json`). |

## V2 Authentication

| Id | Requirement | Outcome | Evidence or reason |
|---|---|---|---|
| V2.1.1 | Passwords of at least 12 characters | ok | `services/identity/internal/password/password_test.go#TestValidatePolicy`. |
| V2.1.2 | Passwords of 64+ accepted, over 128 denied | **problem** | The limit is 1024 characters (`MaxLength` in `services/identity/internal/password/password.go`), so passwords between 129 and 1024 are accepted. [F14] |
| V2.1.3 | No password truncation | ok | The whole string is hashed (`services/identity/internal/password/password_test.go#TestHashFormatAndVerify`). |
| V2.1.4 | Any printable Unicode accepted | ok | Length is counted in runes and no character class is rejected (`services/identity/internal/password/password.go`). |
| V2.1.5 | Users can change password | ok | `services/identity/internal/httpapi/httpapi_integration_test.go#TestChangePassword`. |
| V2.1.6 | Change requires current and new password | ok | `services/identity/internal/httpapi/httpapi_integration_test.go#TestChangePassword` (wrong current password gives 403). |
| V2.1.7 | Breached password check | **problem** | No local or remote breached-password list is consulted at registration, login or change. [F14] |
| V2.1.8 | Password strength meter | **problem** | The web UI has no strength meter (`web/src/pages/ChangePasswordPage.tsx`). [F14] |
| V2.1.9 | No composition rules | ok | `ValidatePolicy` checks length, blank and equal-to-username only (`services/identity/internal/password/password_test.go#TestValidatePolicy`). |
| V2.1.10 | No periodic rotation or history | ok | Not implemented; only the first-login change of the bootstrap password (`services/identity/internal/httpapi/mustchange_integration_test.go#TestMustChangePasswordEnforcement`). |
| V2.1.11 | Paste and password managers permitted | ok | Plain password inputs without paste handlers (`web/src/components/TextField.tsx`). |
| V2.1.12 | Temporarily view the password | ok | Show/Hide toggle in `web/src/components/TextField.tsx` (no dedicated test). |
| V2.2.1 | Anti-automation on authentication | ok | Per-user and per-IP limit with 429: `services/identity/internal/httpapi/httpapi_integration_test.go#TestLoginRateLimit429AndUnblock`, `services/identity/internal/httpapi/httpapi_integration_test.go#TestRateLimitPerIPIgnoresXForwardedFor`, `services/core/internal/stackitest/security_integration_test.go#TestSecurity/brute_force_login`. Limiter is per process. |
| V2.2.2 | Weak authenticators not used as the main factor | ok | SMS and email are not authentication factors; there is no email-based recovery. |
| V2.2.3 | Notification after credential changes | **problem** | No email or notification is sent on password change, token creation or SSH key addition. [F13] |
| V2.3.1 | Generated initial passwords | ok | Bootstrap password is 24 random alphanumerics from the chart and must be changed at first login (`services/identity/internal/users/bootstrap_integration_test.go#TestBootstrapThenChangePasswordClearsMustChange`). |
| V2.3.2 | Support for FIDO/U2F authenticators | **problem** | No second factor of any kind is supported. [F11] |
| V2.3.3 | Renewal reminders for time-bound authenticators | **problem** | Personal tokens have a mandatory expiry (`services/identity/internal/apitokens/apitokens_integration_test.go#TestCreateValidation`) but nobody is warned before they expire. [F13] |
| V2.4.1 | Passwords salted and hashed with an approved KDF | ok | argon2id m=19 MiB t=2 p=1: `services/identity/internal/password/password_test.go#TestHashFormatAndVerify`, `services/identity/internal/auth/auth_integration_test.go#TestRehashOnLogin`. |
| V2.4.2 | Unique salt of at least 32 bits | ok | 16 random bytes per hash: `services/identity/internal/password/password_test.go#TestHashSaltIsRandom`. |
| V2.4.3 | PBKDF2 iteration count | n/a | PBKDF2 is not used (argon2id). |
| V2.4.4 | bcrypt work factor | n/a | bcrypt is not used (argon2id). |
| V2.4.5 | Additional KDF iteration with a secret salt | **problem** | No pepper is applied to password hashes. [F14] |
| V2.5.1 | Initial activation or recovery secret not sent in clear | n/a | The system sends no activation or recovery secret; the admin sets initial passwords and the bootstrap one is in a Kubernetes Secret. |
| V2.5.2 | No password hints or secret questions | ok | None exist in `api/openapi.yaml`. |
| V2.5.3 | Recovery does not reveal the password | ok | Passwords are only stored as hashes; the admin can only set a new one. |
| V2.5.4 | No shared or default accounts | ok | The default username `admin` has a per-installation random password with forced change (`deploy/gitstack/templates/identity/admin-secret.yaml`, `services/identity/internal/users/bootstrap_integration_test.go#TestBootstrapThenChangePasswordClearsMustChange`). |
| V2.5.5 | User notified when an authentication factor changes | **problem** | Same gap as V2.2.3: an admin password reset or a changed password raises no notification. [F13] |
| V2.5.6 | Secure recovery mechanism | n/a | There is no self-service forgotten-password flow; recovery is an admin password reset (P5). |
| V2.5.7 | Identity proofing when OTP/MFA is lost | n/a | No OTP or MFA exists. |
| V2.6.1 | Lookup secrets single use | n/a | No lookup secrets (recovery codes) exist. |
| V2.6.2 | Lookup secret randomness | n/a | No lookup secrets exist. |
| V2.6.3 | Lookup secrets resist offline attack | n/a | No lookup secrets exist. |
| V2.7.1 | Out-of-band authenticators | n/a | No out-of-band authenticator exists. |
| V2.7.2 | Out-of-band codes expire after 10 minutes | n/a | No out-of-band authenticator exists. |
| V2.7.3 | Out-of-band codes single use | n/a | No out-of-band authenticator exists. |
| V2.7.4 | Out-of-band secure channel | n/a | No out-of-band authenticator exists. |
| V2.7.5 | Out-of-band code stored hashed | n/a | No out-of-band authenticator exists. |
| V2.7.6 | Out-of-band code entropy | n/a | No out-of-band authenticator exists. |
| V2.8.1 | TOTP lifetime | n/a | No OTP exists. |
| V2.8.2 | OTP keys protected | n/a | No OTP exists. |
| V2.8.3 | OTP algorithms | n/a | No OTP exists. |
| V2.8.4 | TOTP single use | n/a | No OTP exists. |
| V2.8.5 | TOTP reuse logged | n/a | No OTP exists. |
| V2.8.6 | Physical OTP generator revocable | n/a | No OTP exists. |
| V2.8.7 | Biometrics only as secondary factor | n/a | No biometric authenticator exists. |
| V2.9.1 | Cryptographic verification keys protected | n/a | No cryptographic device authenticators exist (SSH public keys are stored as public data and only grant Git access). |
| V2.9.2 | Challenge nonce length | n/a | No cryptographic device authenticators exist. |
| V2.9.3 | Cryptographic device algorithms | n/a | No cryptographic device authenticators exist. |
| V2.10.1 | Intra-service secrets not static | **problem** | One static secret shared by gateway, identity, core and git, never rotated and used both as bearer and as HMAC key. [F9] |
| V2.10.2 | No default service credentials | ok | Service secret and database password are generated per release (`deploy/gitstack/templates/identity/service-secret.yaml`, `deploy/gitstack/templates/postgres/secret.yaml`). |
| V2.10.3 | Service passwords protected at rest | ok | Webhook secrets and mirror credentials are AES-GCM encrypted (`services/core/internal/webhooks/unit_test.go#TestKeyring_CifraDecifraERotazione`); service secrets rely on Kubernetes Secrets (see F2). |
| V2.10.4 | No secrets in source | ok | Generated by the chart, scanned by gosec (`.github/workflows/security.yml`); bootstrap password never logged: `services/identity/bootstrap_test.go#TestBootstrapAdminNeverLogsPassword`. |

## V3 Session Management

| Id | Requirement | Outcome | Evidence or reason |
|---|---|---|---|
| V3.1.1 | Session tokens never in URLs | ok | Credentials come from the `Authorization` header or the cookie only (`services/gateway/internal/middleware/auth.go#credentialFrom`). |
| V3.2.1 | New session token at authentication | ok | `Create` makes a fresh random value at every login and there is no pre-login session to fixate (`services/identity/internal/sessions/sessions.go`). |
| V3.2.2 | At least 64 bits of entropy | ok | 32 bytes from `crypto/rand` (`services/identity/internal/sessions/sessions.go`). |
| V3.2.3 | Tokens stored only in secure cookies | ok | HttpOnly cookie only; no session data in web storage (`services/identity/internal/auth/auth.go#SessionCookie`). |
| V3.2.4 | Approved algorithms | ok | `crypto/rand` and a SHA-256 stored hash (`services/identity/internal/tokens/tokens_test.go#TestHashBytes`). |
| V3.3.1 | Logout and expiry invalidate the session | ok | `services/identity/internal/httpapi/httpapi_integration_test.go#TestSessionAndLogout`, `services/identity/internal/auth/auth_integration_test.go#TestSessionExpiry`, `services/gateway/internal/httpserver/logout_scopes_test.go#TestAuth_LogoutSvuotaLaCache`. |
| V3.3.2 | Periodic re-authentication or idle timeout | **problem** | Sessions have only an absolute 7-day TTL and no idle timeout (`DefaultTTL` in `services/identity/internal/sessions/sessions.go`). [F12] |
| V3.3.3 | Other sessions ended after password change | ok | `services/identity/internal/auth/auth_integration_test.go#TestChangePasswordRevokesSessions`, `services/core/internal/stackitest/security_integration_test.go#TestSecurity/sessione_dopo_cambio_password`. |
| V3.3.4 | Users can list and end their sessions | **problem** | There is no endpoint or screen to list or revoke individual sessions. [F12] |
| V3.4.1 | Secure attribute | ok | Set whenever the request is HTTPS through a trusted proxy: `services/identity/internal/httpapi/httpapi_integration_test.go#TestCookieSecureDaProxyFidato`. |
| V3.4.2 | HttpOnly attribute | ok | `services/identity/internal/auth/auth.go#SessionCookie`. |
| V3.4.3 | SameSite attribute | ok | `SameSite=Lax` in `services/identity/internal/auth/auth.go#SessionCookie`. |
| V3.4.4 | __Host- cookie prefix | **problem** | The cookie is named `gst_session` without the prefix. [F16] |
| V3.4.5 | Cookie path | ok | `Path=/` is the narrowest path since GitStack owns the whole host. |
| V3.5.1 | Revoke OAuth tokens of linked apps | n/a | GitStack is only an OIDC client and exposes no OAuth provider or third-party app grants. |
| V3.5.2 | Session tokens rather than static API secrets | ok | The web uses session cookies; personal tokens exist for Git and CLI only, with mandatory expiry, scopes and revocation (`services/identity/internal/apitokens/apitokens_integration_test.go#TestRevokeAndList`). |
| V3.5.3 | Stateless tokens protected | ok | The only stateless tokens are OIDC ID tokens (`services/identity/internal/oidc/service_test.go#TestFinish_TokenValidation`) and the HMAC identity header with timestamp (`services/core/internal/trust/trust_test.go#TestVerify_Rifiuti`). |
| V3.7.1 | Re-authentication for sensitive transactions | ok | Password change needs the current password (`services/identity/internal/httpapi/httpapi_integration_test.go#TestChangePassword`); repository deletion is recoverable for 7 days (`services/core/internal/httpserver/repos_trash_integration_test.go#TestRepos_EliminazioneERipristino`). |

## V4 Access Control

| Id | Requirement | Outcome | Evidence or reason |
|---|---|---|---|
| V4.1.1 | Access control on a trusted layer | ok | `services/core/internal/stackitest/security_integration_test.go#TestSecurity`. |
| V4.1.2 | Access control attributes not user-manipulable | ok | Visibility and grants are changed only by admin operations: `services/identity/internal/httpapi/owner_visibility_integration_test.go#TestOwnerAndVisibilityRules`; unknown JSON fields rejected. |
| V4.1.3 | Least privilege | ok | A personal token cannot create tokens (own or an agent's) with scopes it does not grant: 403 `insufficient_scope`, checked with `pkg/scopes.Grants`; browser sessions keep full scopes (resolved F1, GIT-227): `services/identity/internal/httpapi/token_scope_ceiling_integration_test.go#TestCreateToken_ScopesNotBeyondCaller`, `services/identity/internal/httpapi/token_scope_ceiling_integration_test.go#TestCreateAgentToken_ScopesNotBeyondCaller`. |
| V4.1.5 | Access control fails securely | ok | `services/gateway/internal/httpserver/auth_test.go#TestAuth_IdentityGiuNonAprePerErrore`, `services/gateway/internal/httpserver/permissions_test.go#TestPermessi_SenzaCheckerEPermessoRichiesto503`. |
| V4.2.1 | IDOR | ok | Denied and non-existent resources look the same: `services/gateway/internal/httpserver/permissions_test.go#TestPermessi_NegatoUgualeSeLaRisorsaNonEsiste`, `services/core/internal/stackitest/security_integration_test.go#TestSecurity/risorsa_di_altra_organizzazione`, `services/core/internal/httpserver/attachments_integration_test.go#TestAttachments_IssueNascosta`. |
| V4.2.2 | Anti-CSRF mechanism | **problem** | Cookie-authenticated API relies only on SameSite=Lax: no Origin or Fetch-Metadata check, no token. [F16] |
| V4.3.1 | Multi-factor for administrative interfaces | **problem** | The installation admin signs in with password only. [F11] |
| V4.3.2 | No directory browsing | ok | nginx has no autoindex and only serves the built SPA and explicit downloads (`web/src/lib/nginxConf.test.ts#sta prima del fallback SPA`). |
| V4.3.3 | Step-up or segregation of duties | n/a | No high-value transactional functions; destructive operations ask to retype the name (G8). |

## V5 Validation, Sanitization and Encoding

| Id | Requirement | Outcome | Evidence or reason |
|---|---|---|---|
| V5.1.1 | HTTP parameter pollution | ok | Parameters are bound once from path/query into typed values by generated code (`services/core/internal/openapi/oapi-codegen.yaml`); JSON bodies are single source. |
| V5.1.2 | Mass assignment | ok | Dedicated input types, unknown fields rejected (`services/core/internal/httpserver/resources.go#CreateResource`), `additionalProperties: false` in `api/openapi.yaml`. |
| V5.1.3 | Positive validation | ok | `pkg/names/names_test.go#TestValidateRepoName`, `services/git/internal/gitref/gitref_test.go#TestValidateRef`, `services/git/internal/gitref/gitref_test.go#TestValidatePath`. |
| V5.1.4 | Structured data typed and schema-validated | ok | Types generated from `api/openapi.yaml`, contract drift checked by `.github/workflows/api-contract.yml`; `services/core/internal/httpserver/issues_integration_test.go#TestIssues_CreazioneRegoleI3EValidazione`. |
| V5.1.5 | Redirects restricted | ok | OIDC `redirectTo` only relative paths (`services/identity/internal/oidc/service_test.go#TestStart_Errors`); mirrors never follow redirects (`services/git/internal/mirrorpush/mirrorpush.go#pushArgs`); webhook redirects re-checked (`pkg/egress/egress_test.go#TestRedirects`). |
| V5.2.1 | HTML sanitised | ok | `web/src/components/Markdown.test.tsx#neutralizza i payload XSS noti`, `web/src/components/Markdown.test.tsx#ammette details, summary, sub, sup, kbd, br, img; rimuove script, iframe, style, on*`. |
| V5.2.2 | Unstructured data sanitised | ok | Length limits in the contract; NUL and invalid UTF-8 rejected on search text (`services/core/internal/httpserver/issues_search.go`) and clipped in stored log text (`services/core/internal/webhooks/unit_test.go#TestClipUTF8`). |
| V5.2.3 | SMTP injection | ok | Subject is flattened and Q-encoded, addresses parsed with `net/mail` (`services/core/internal/mailer/mailer.go#oneLine`); no dedicated test. |
| V5.2.4 | No dynamic code execution | ok | No `eval`/`new Function` in `web/src` and none in Go; `eslint-plugin-security` runs in CI (`web/eslint.security.config.js`). |
| V5.2.5 | Template injection | n/a | No server-side template engine receives user input (emails are built with fixed formats). |
| V5.2.6 | SSRF | ok | `pkg/egress/egress_test.go#TestDNSResolvingToBlockedAddress`, `services/core/internal/stackitest/m06_integration_test.go#TestM06E2E/destinazioni_ssrf_bloccate`, `services/git/internal/mirrorpull/mirrorpull_test.go#TestPull_DestinazioniVietateSenzaConnessione`. See F7 for the residual LAN exposure. |
| V5.2.7 | SVG | ok | Raw SVG is an attachment (`services/git/internal/gitread/code_test.go#TestRaw_SafeTypes`), uploads reject SVG (`services/core/internal/attachments/attachments_test.go#TestDetect`), file view uses `<img>` only (`web/src/pages/repos/FileView.test.tsx#SVG: mostrato solo come <img>, il markup non entra nella pagina`). |
| V5.2.8 | Scriptable markup languages | ok | `web/src/components/Markdown.test.tsx#neutralizza i payload XSS noti`; CSS and style attributes are removed. |
| V5.3.1 | Context-aware output encoding | ok | React for HTML, `encoding/json` for JSON, RFC 6266 for headers (`services/core/internal/httpserver/attachments_integration_test.go#TestAttachments_NomeFileNelleIntestazioni`). |
| V5.3.2 | Unicode preserved | ok | UTF-8 end to end; filenames encoded per RFC 6266 (`services/core/internal/httpserver/attachments_integration_test.go#TestAttachments_NomeFileNelleIntestazioni`). |
| V5.3.3 | XSS protection | ok | React escaping plus sanitiser (`web/src/components/Markdown.test.tsx#neutralizza i payload XSS noti`). No CSP exists as a second layer (see F3). |
| V5.3.4 | Parameterised queries | ok | pgx placeholders everywhere; the only built SQL splices fixed fragments and `$n` positions (`services/core/internal/store/repos.go`, `services/core/internal/store/resource.go`). |
| V5.3.5 | Escaping where parameters are absent | n/a | There is no query built from user text. |
| V5.3.6 | JSON injection and eval | ok | JSON is produced by `encoding/json` and read with `fetch().json()`; no `eval` in `web/src`. |
| V5.3.7 | LDAP injection | n/a | No LDAP. |
| V5.3.8 | OS command injection | ok | Fixed argv, validated refs/paths, `--` separators and `-F` search: `services/git/internal/gitref/gitref_test.go#TestValidateRef`, `services/git/internal/gitread/search_test.go#TestSearchCode_LiteralText`, `services/git/internal/gitrun/gitrun_test.go#TestEnvStripsGit`, `services/git/internal/mirrorpull/mirrorpull_test.go#TestPull_ArgvSenzaCredenzialiNeForce`. |
| V5.3.9 | LFI and RFI | ok | Repository dirs derive from UUIDs, names are validated, attachments are stored by id: `services/git/internal/repostore/repostore_test.go#TestCreate_Rifiuti`, `services/git/internal/gitref/gitref_test.go#TestValidatePath`. |
| V5.3.10 | XPath and XML injection | n/a | No XML or XPath processing (no `encoding/xml` import). |
| V5.4.1 | Memory-safe string and buffer handling | n/a | Go and TypeScript are memory-safe; the `git` binary is the upstream one. |
| V5.4.2 | Format strings | n/a | Not exploitable in Go; vet checks printf verbs in lint. |
| V5.4.3 | Integer overflow | n/a | Go integers wrap without memory impact; sizes and pages are bounded in handlers (`services/identity/internal/httpapi/tokens_keys.go#page`). |
| V5.5.1 | Serialized objects with integrity | n/a | No native object serialization is exchanged; see V1.5.2. |
| V5.5.2 | XML parser hardening | n/a | No XML parser is used. |
| V5.5.3 | Deserialization of untrusted data | ok | JSON into typed structs with unknown fields rejected: `services/core/internal/httpserver/resources.go#CreateResource`. |
| V5.5.4 | JSON.parse in browsers | ok | `fetch().json()` in the generated client (`web/src/lib/http.ts`); no `eval`. |

## V6 Stored Cryptography

| Id | Requirement | Outcome | Evidence or reason |
|---|---|---|---|
| V6.1.1 | Regulated private data encrypted at rest | **problem** | User names and emails are personal data stored in clear in Postgres; neither application-level encryption nor guidance on disk and backup encryption exists. [F20] |
| V6.1.2 | Health data | n/a | GitStack does not process health data. |
| V6.1.3 | Financial data | n/a | GitStack does not process financial data. |
| V6.2.1 | Cryptographic modules fail securely | ok | AES-GCM authenticated decryption returns an error and no partial plaintext: `services/core/internal/webhooks/unit_test.go#TestKeyring_CifraDecifraERotazione`, `services/identity/internal/oidc/crypto_test.go`. |
| V6.2.2 | Proven algorithms and libraries | ok | Standard library AES-GCM, HMAC-SHA256, `crypto/rand`; `golang.org/x/crypto/argon2` (`services/identity/internal/password/password.go`). |
| V6.2.3 | IV and mode configuration | ok | AES-GCM with a random nonce per message bound to the webhook id (`services/core/internal/webhooks/crypto.go#Seal`). |
| V6.2.4 | Algorithms can be upgraded | ok | Hash parameters are in the PHC string and re-hashed at login (`services/identity/internal/auth/auth_integration_test.go#TestRehashOnLogin`); key ids in the keyring. |
| V6.2.5 | No insecure primitives | ok | No MD5, SHA-1, DES or RC4 import in Go code (`git grep` empty); SHA-1 only appears as a UUID namespace hash for event ids in `services/core/internal/webhooks/push.go`. |
| V6.2.6 | Nonces not reused | ok | Random nonce per `Seal` (`services/core/internal/webhooks/crypto.go#Seal`). |
| V6.3.1 | CSPRNG for random values | ok | `crypto/rand` for sessions, tokens, salts, OIDC state and nonce; no `math/rand` import in the services (`git grep` empty). |
| V6.3.2 | Random GUIDs v4 | ok | Security-relevant ids use `uuid.New()` (v4, CSPRNG); v7 is used only for resource ids (`services/core/internal/store/resource.go`), which are not secrets. |
| V6.4.1 | Secrets management solution | **problem** | Kubernetes Secrets without encryption at rest and readable through the world-readable kubeconfig. [F2] |
| V6.4.2 | Key material isolated from the application | **problem** | Keys are environment variables of the application pods; no vault or KMS. [F2] |

## V7 Error Handling and Logging

| Id | Requirement | Outcome | Evidence or reason |
|---|---|---|---|
| V7.1.1 | No credentials in logs | ok | `services/identity/integration_bootstrap_test.go#TestBootstrapAdminRealDBNoPasswordInLogs`, `services/identity/internal/db/db_test.go#TestOpen_NoPasswordInError`, `services/git/internal/httpserver/mirror_test.go#TestMirrorPush_IlTokenNonEsceMai`; the request log keeps only the path (`services/gateway/internal/middleware/logging_test.go#TestLogging_UnaRigaJSONConCampiAttesi`). |
| V7.1.2 | No other sensitive data in logs | ok | Same tests; payloads, queries and bodies are never logged by the gateway. |
| V7.1.3 | Security events logged | **problem** | Identity logs only internal errors: there is no event for login success or failure, password change, token or key changes, grants. [F10] |
| V7.1.4 | Log events carry investigation data | **problem** | The gateway log has no user id or token id and `remote_addr` is the proxy address, not the client. [F10] |
| V7.2.1 | Authentication decisions logged | **problem** | No log line for login, logout, 401 reasons or token use. [F10] |
| V7.2.2 | Access control decisions logged | **problem** | 403 results are only a status in the request log, without user or resource. [F10] |
| V7.3.1 | Log injection | ok | JSON handler escapes values (`services/gateway/main.go`). |
| V7.3.3 | Logs protected from unauthorised access | **problem** | `kubectl logs` needs the kubeconfig, which the installer makes world-readable. [F2] |
| V7.3.4 | Time sources | ok | Container clocks are UTC and timestamps are RFC 3339 from `slog`; clock sync is delegated to the host OS. |
| V7.4.1 | Generic error messages | ok | Internal errors return a generic body and log the detail (`services/identity/internal/httpapi/httpapi.go#internal`, `services/core/internal/httpserver/attachments.go`). |
| V7.4.2 | Error handling across the code | ok | Explicit error returns with fail-closed mapping: `services/gateway/internal/httpserver/auth_test.go#TestAuth_IdentityGiuNonAprePerErrore`. |
| V7.4.3 | Last-resort error handler | **problem** | No recover middleware: a panic closes the connection with no body and no structured log line. [F10] |

## V8 Data Protection

| Id | Requirement | Outcome | Evidence or reason |
|---|---|---|---|
| V8.1.1 | No sensitive data cached by server components | ok | The gateway cache holds only the credential decision for 30 s (`services/gateway/internal/identityclient/cache.go`, `services/gateway/internal/identityclient/cache_test.go#TestCache_NegativoCinqueSecondi`); no response caching. |
| V8.1.2 | Temporary copies purged | ok | Upload temp files are renamed or removed (`services/core/internal/attachments/attachments_test.go#TestDisk_SaveLimiteERimozione`), expired sessions are deleted (`services/identity/internal/sessions/sessions.go#DeleteExpired`), delivery logs expire after 30 days (`services/core/internal/httpserver/webhooks_integration_test.go#TestWebhooks_LogConservatoTrentaGiorni`). |
| V8.1.3 | Few request parameters | ok | No hidden fields; the only state is the cookie or the bearer token. |
| V8.1.4 | Abnormal request volumes detected | **ok** | The gateway counts and logs rejected requests per credential and per IP (`rate limit exceeded`, obfuscated key) so an alert can be set (`services/gateway/internal/middleware/ratelimit.go`, `services/gateway/internal/middleware/ratelimit_test.go#TestRateLimit_PerTokenAndPerIP`). Git endpoints served outside the gateway keep the residual limit in `docs/operations.md`. |
| V8.2.1 | Anti-caching headers | **ok** | Gateway middleware adds `Cache-Control: no-store` on authenticated responses; raw/archive set it too: `services/gateway/internal/middleware/headers.go`, `services/git/internal/httpserver/code.go`, `services/core/internal/httpserver/repos_code.go`. |
| V8.2.2 | No sensitive data in browser storage | ok | Only the theme is kept in `localStorage` (`web/src/pages/ComponentsPage.tsx`). |
| V8.2.3 | Data cleared at session end | ok | Logout navigates to the login route and unmounts page state; nothing is persisted (`web/src/pages/ChangePasswordPage.tsx#handleLogout`). |
| V8.3.1 | Sensitive data not in query strings | ok | Tokens use the `Authorization` header and passwords the JSON body; the OIDC callback carries a single-use code and state only (`services/identity/internal/oidc/service_test.go#TestFinish_StateChecks`). |
| V8.3.2 | Users can remove or export their data | **problem** | An admin can delete a user (`services/identity/internal/httpapi/httpapi_integration_test.go#TestDeleteUser`), but no export and no self-service removal exist. [F20] |
| V8.3.3 | Privacy notice and consent | n/a | Accounts are created by the instance admin for its own members; the product collects no data for third-party use and has no telemetry. |
| V8.3.4 | Sensitive data identified and policy defined | ok | `docs/security/threat-model.md` asset tables and the rules in `docs/rules-coverage.md`. |
| V8.3.5 | Audit of access to regulated data | n/a | No regulated data classification applies; general auditing is F10. |
| V8.3.6 | Memory overwritten after use | n/a | Not feasible in Go (immutable strings, GC); a memory dump needs root on the host. |
| V8.3.7 | Encrypted with approved algorithms | ok | AES-256-GCM: `services/core/internal/webhooks/unit_test.go#TestKeyring_CifraDecifraERotazione`, `services/identity/internal/oidc/crypto_test.go`. |
| V8.3.8 | Retention classification | ok | Trash 7 days, notifications, delivery logs 30 days, mirror logs 50 runs or 30 days: `services/core/internal/httpserver/repos_trash_integration_test.go#TestRepos_EliminazioneERipristino`, `services/core/internal/httpserver/mirrors_pull_engine_integration_test.go#TestPullMirror_Registro_50E30Giorni`. |

## V9 Communication

| Id | Requirement | Outcome | Evidence or reason |
|---|---|---|---|
| V9.1.1 | TLS for all client connectivity | ok | HTTPS by default with a permanent redirect from port 80; plain HTTP only with explicit `--insecure-http` and a UI warning: `deploy/gitstack/templates/ingress-redirect.yaml`, `deploy/test-vm/tests/tls.Tests.ps1`, `web/src/lib/insecureHttp.ts`; `docs/tls.md`. |
| V9.1.2 | Only strong cipher suites | **problem** | No Traefik TLSOption pins cipher suites and no TLS test tool is run in CI. [F3] |
| V9.1.3 | Only TLS 1.2 and 1.3 | **problem** | The chart does not pin a minimum version; it relies on Traefik defaults. [F3] |
| V9.2.1 | Trusted TLS certificates on all connections | **problem** | External peers (webhooks, mirrors, OIDC, SMTP) are verified; the in-cluster hops and the bundled Postgres are not encrypted. [F8] |
| V9.2.2 | Encryption on all connections | **problem** | Gateway to services, services to NATS and bundled Postgres are cleartext. [F8] |
| V9.2.3 | External encrypted connections authenticated | ok | Mirrors use a token over HTTPS (`services/git/internal/mirrorpush/mirrorpush_test.go#TestArgvSenzaToken`), SMTP uses STARTTLS and credentials (`services/core/internal/mailer/mailer_test.go#TestSend_StartTLSObbligatorioSeRichiesto`), OIDC uses a client secret. |
| V9.2.4 | Certificate revocation checking | n/a | Internal CA with rotation by `gitstack-tls` and Let's Encrypt via Traefik; no OCSP responder is operated. |

## V10 Malicious Code

| Id | Requirement | Outcome | Evidence or reason |
|---|---|---|---|
| V10.2.1 | No unauthorised phone-home | ok | No analytics or telemetry code in services, web, CLI or admin (`git grep telemetry` is empty); outgoing calls are webhooks, mirrors, SMTP and OIDC configured by admins. |
| V10.2.2 | Sensors and privacy permissions | n/a | A server application with a web SPA; no sensor or contact permissions. |
| V10.3.1 | Auto-update over secure channels with signatures | ok | `admin/internal/upgrade/verify_test.go#TestV5RifiutaBinarioFirmatoDaAltraIdentita`, `deploy/tests/v5_verify_test.sh#binario alterato`. |
| V10.3.2 | Integrity of loaded code | ok | No remote script, font or style is loaded: the SPA bundle is built into the image (`web/Dockerfile`), dependencies are locked by `web/pnpm-lock.yaml` and `go.work.sum`. |
| V10.3.3 | Subdomain takeover | n/a | GitStack depends on no DNS entries of its own. |

## V11 Business Logic

| Id | Requirement | Outcome | Evidence or reason |
|---|---|---|---|
| V11.1.1 | Sequential business flows | ok | Password-change-first enforcement (`services/identity/internal/httpapi/mustchange_integration_test.go#TestMustChangePasswordEnforcement`), OIDC state (`services/identity/internal/oidc/service_test.go#TestFinish_StateChecks`). |
| V11.1.2 | Human-time flows | n/a | There is no business flow that depends on human timing. |
| V11.1.3 | Per-action limits | ok | Push file limit R6 (`services/git/internal/smarthttp/receive_test.go#TestPushFileOltreLaSogliaRifiutato`), upload size (`services/core/internal/httpserver/attachments_integration_test.go#TestAttachments_UploadRifiuti`), assignees (`services/core/internal/httpserver/issues_assignees_integration_test.go#TestAssignees_LimiteEPermessiI6`), search results (`services/git/internal/gitread/search_test.go#TestSearchCode_LimitReached`). |
| V11.1.4 | Anti-automation for excessive calls | **problem** | The gateway now limits calls per credential and per client IP with 429 and `Retry-After` (resolved F6, GIT-232: `services/gateway/internal/middleware/ratelimit_test.go#TestRateLimit_PerTokenAndPerIP`), but most JSON bodies still have no size ceiling. [F5] |
| V11.1.5 | Limits from threat analysis | ok | Webhook, mirror and push limits derive from rules C7, MP4, R6 and are tested: `services/core/internal/httpserver/mirrors_integration_test.go#TestMirrors_UnSoloPushPerMirror`. |
| V11.1.6 | TOCTOU and races | ok | Egress decision at dial time (`pkg/egress/egress_test.go#TestControlBlocksAtConnect`), pinned IP for mirrors (`services/git/internal/mirrorpull/mirrorpull_test.go#TestPull_DestinazioniVietateSenzaConnessione`), single push per mirror (`services/core/internal/httpserver/mirrors_integration_test.go#TestMirrors_UnSoloPushPerMirror`). |
| V11.1.7 | Monitoring of unusual business events | **problem** | No audit trail or anomaly detection. [F10] |
| V11.1.8 | Configurable alerting | **ok** | Thresholds are configurable in the chart (`gateway.env.rateLimit.*`) and rejections are logged with a stable message and counted (`services/gateway/internal/middleware/ratelimit_test.go#TestRateLimit_PerTokenAndPerIP`); alerting itself is done on that log line, see `docs/operations.md`. |

## V12 Files and Resources

| Id | Requirement | Outcome | Evidence or reason |
|---|---|---|---|
| V12.1.1 | Large files cannot fill the storage | **problem** | Attachments are capped (`services/core/internal/httpserver/attachments_integration_test.go#TestAttachments_UploadRifiuti`) but pushes have only a per-file limit (R6) and mirror pull has no size cap. [F18] |
| V12.1.2 | Compressed file expansion limits | n/a | ZIP attachments are stored as opaque files and never expanded on the server. |
| V12.1.3 | Per-user file quota | **problem** | No quota or count limit on attachments per user or repo. [F17] |
| V12.2.1 | Type validated from content | ok | `services/core/internal/attachments/attachments_test.go#TestDetect`. |
| V12.3.1 | Filenames not used in paths | ok | Attachments are stored as `<repo_id>/<id>` (`services/core/internal/attachments/attachments_test.go#TestDisk_SaveLimiteERimozione`). |
| V12.3.2 | Filenames cannot disclose local files | ok | Same, and repo paths pass `ValidatePath` (`services/git/internal/gitref/gitref_test.go#TestValidatePath`). |
| V12.3.3 | Filenames cannot cause RFI or SSRF | ok | Filenames are never fetched or included. |
| V12.3.4 | Reflective file download | ok | API responses are JSON and download names are sanitised: `services/git/internal/gitread/code_test.go#TestRaw_SafeTypes`. |
| V12.3.5 | Filenames not passed to OS commands | ok | Paths are validated and follow `--`: `services/git/internal/gitref/gitref_test.go#TestValidatePath`. |
| V12.3.6 | No code from untrusted sources | ok | No CDN or runtime plugin loading; lockfiles and checksums (`web/pnpm-lock.yaml`, `go.work.sum`). |
| V12.4.1 | Uploaded files outside the web root | ok | Dedicated volume mounted only into core (`deploy/gitstack/templates/core/attachments-pvc.yaml`). |
| V12.4.2 | Antivirus scanning of uploads | **problem** | No scan of attachments or repository content. [F17] |
| V12.5.1 | Only specific files served by the web tier | ok | nginx serves the SPA build and explicit download locations: `web/src/lib/nginxConf.test.ts#sta prima del fallback SPA`. |
| V12.5.2 | Uploaded files never executed | ok | `services/git/internal/gitread/code_test.go#TestRaw_SafeTypes`, `services/core/internal/stackitest/code_reads_integration_test.go#TestCodeReads/raw_header_di_sicurezza`, `services/core/internal/httpserver/attachments_integration_test.go#TestAttachments_UploadEDownload`. |
| V12.6.1 | Allow list of destinations for server requests | ok | The node's own addresses and the Kubernetes ports (6443, 10250, 10255, 2379-2380 on private addresses) are always blocked; the LAN ranges webhooks may reach are listed explicitly with `egress.allow` (`install.sh --egress-allow`): `pkg/egress/egress_test.go#TestNodeAddressesAreBlockedByDefault`, `services/core/internal/stackitest/m06_integration_test.go#TestM06E2E/webhook_to_node_address_blocked` (resolved F7, GIT-233). |

## V13 API and Web Service

| Id | Requirement | Outcome | Evidence or reason |
|---|---|---|---|
| V13.1.1 | Same parsers everywhere | ok | `net/url` and `net/netip` are used for every check: `services/core/internal/mirrors/mirrors_test.go#TestParseURL`, `pkg/egress/egress_test.go#TestCheckIPCategories`. |
| V13.1.3 | No sensitive data in API URLs | ok | Credentials never travel in the path or query; request log keeps only the path (`services/gateway/internal/middleware/logging_test.go#TestLogging_UnaRigaJSONConCampiAttesi`). |
| V13.1.4 | Authorisation at URI and resource level | ok | `services/gateway/internal/httpserver/permissions_test.go#TestPermessi_TabellaMiddleware`, `services/core/internal/httpserver/code_reads_integration_test.go#TestCodeReads_Permessi`. |
| V13.1.5 | Unexpected content types rejected | **problem** | JSON bodies are decoded without checking `Content-Type`; no 415 in core, identity or gateway. [F15] |
| V13.2.1 | HTTP methods match the action | ok | Only contract operations are routed: `services/gateway/internal/httpserver/auth_test.go#TestAuth_TabellaDiRotte`. |
| V13.2.2 | Schema validation before accepting input | ok | Typed decode, unknown fields rejected, per-field validation: `services/identity/internal/apitokens/apitokens_integration_test.go#TestCreateValidation`, `services/core/internal/httpserver/issues_integration_test.go#TestIssues_CreazioneRegoleI3EValidazione`. |
| V13.2.3 | CSRF protection for cookie-authenticated REST | **problem** | See V4.2.2. [F16] |
| V13.2.5 | Explicit Content-Type check | **problem** | Same gap as V13.1.5. [F15] |
| V13.2.6 | Headers and payload trustworthy | ok | Identity headers are HMAC-signed with a timestamp and client copies are stripped: `services/gateway/internal/trust/trust_test.go#TestStripClientHeaders`, `services/core/internal/stackitest/security_integration_test.go#TestSecurity/header_identita_falsificati`. |
| V13.3.1 | SOAP/XML schema validation | n/a | No SOAP or XML service. |
| V13.3.2 | WS-Security | n/a | No SOAP service. |
| V13.4.1 | GraphQL query limits | n/a | No GraphQL. |
| V13.4.2 | GraphQL authorisation | n/a | No GraphQL. |

## V14 Configuration

| Id | Requirement | Outcome | Evidence or reason |
|---|---|---|---|
| V14.1.1 | Secure repeatable build and deployment | ok | `.github/workflows/ci.yml`, `deploy/install.sh`, `.github/workflows/install.yml`. |
| V14.1.2 | Compiler hardening flags | n/a | Go and TypeScript are memory-safe; Go tests run with `-race` in CI. |
| V14.1.3 | Server configuration hardened | **problem** | nginx exposes its version and sets no security headers; k3s kubeconfig is world-readable. [F3] |
| V14.1.4 | Redeployable from a runbook | ok | `docs/operations.md`, `.github/workflows/install.yml` (install, upgrade, restore). |
| V14.2.1 | Dependencies kept up to date and checked | ok | `.github/workflows/security.yml`, `docs/security-scans.md`. |
| V14.2.2 | Unneeded features removed | **problem** | The component showcase is routed in production at `/_components` (`web/src/App.tsx`). [F19] |
| V14.2.3 | External assets with SRI | n/a | No asset is hosted externally. |
| V14.2.4 | Components from trusted repositories | ok | Go modules with `go.work.sum` and npm with `web/pnpm-lock.yaml`; pinned base images in each Dockerfile. |
| V14.2.5 | SBOM maintained | ok | Per-release SBOM with syft in `.github/workflows/ci.yml` (job `sign`). |
| V14.2.6 | Sandboxing third-party libraries | n/a | Not practical for in-process Go and JS libraries; mitigated by scans and distroless images. |
| V14.3.2 | Debug modes disabled | ok | No `pprof` or debug endpoint (`git grep net/http/pprof` is empty); production Vite build. |
| V14.3.3 | No version information in headers | **problem** | The nginx image answers with `Server: nginx/<version>` (`web/deploy/nginx.conf.template` has no `server_tokens off`). [F3] |
| V14.4.1 | Content-Type on every response | ok | JSON writers and raw/attachments set it explicitly: `services/git/internal/gitread/code_test.go#TestRaw_SafeTypes`; nginx uses `default_type`. |
| V14.4.2 | Content-Disposition on API responses | **problem** | JSON API responses do not carry `Content-Disposition`; the F4 fix addressed Cache-Control and nosniff only. |
| V14.4.3 | Content-Security-Policy | **problem** | Only raw/attachment responses carry `sandbox`; the SPA and API send no CSP. [F3] |
| V14.4.4 | X-Content-Type-Options nosniff | **problem** | Set on raw, attachments and downloads only, not on SPA and API responses. [F3] |
| V14.4.5 | Strict-Transport-Security | **problem** | No HSTS header is configured in nginx or Traefik. [F3] |
| V14.4.6 | Referrer-Policy | **problem** | No Referrer-Policy header; only Markdown links set `no-referrer` per element. [F3] |
| V14.4.7 | Embedding disabled by default | **problem** | No `frame-ancestors` or `X-Frame-Options`: the SPA can be framed. [F3] |
| V14.5.1 | Only the HTTP methods in use | ok | Unknown route or method gets 404 and a warning log: `services/gateway/internal/httpserver/auth_test.go#TestAuth_TabellaDiRotte`. |
| V14.5.2 | Origin header not used for decisions | ok | No code reads the `Origin` header. |
| V14.5.3 | CORS strict | ok | No `Access-Control-*` header is ever emitted: same-origin only (`git grep Access-Control` over Go code is empty). |
| V14.5.4 | Proxy-added headers authenticated | ok | Forwarded headers are honoured only from trusted CIDRs and identity headers are signed: `services/identity/internal/httpapi/httpapi_integration_test.go#TestLoginIPFidato`, `services/gateway/internal/trust/trust_test.go#TestStripClientHeaders`. |

## Findings register

One row per correction item. Severity: critical (exploitable by an unauthenticated attacker with large impact), high (privilege escalation or full compromise with a realistic precondition), medium (weakens a defence or exposes data under a plausible condition), low (hardening or missing control with limited impact). Tests marked *(new)* do not exist yet: the correction item adds them.

| Id | Severity | Area and requirements | Files | Test that would prove it |
|---|---|---|---|---|
| F1 | high | **Resolved (GIT-227)**: personal token scope escalation (V4.1.3) | `services/identity/internal/httpapi/tokens_keys.go`<br>`services/identity/internal/httpapi/user_tokens.go`<br>`services/identity/internal/apitokens/apitokens.go`<br>`api/openapi.yaml` | `services/identity/internal/httpapi/token_scope_ceiling_integration_test.go#TestCreateToken_ScopesNotBeyondCaller`<br>`services/identity/internal/httpapi/token_scope_ceiling_integration_test.go#TestCreateAgentToken_ScopesNotBeyondCaller` |
| F2 | high | **Resolved (GIT-228).** Host and cluster secrets exposure (V1.6.2, V6.4.1, V6.4.2, V7.3.3) | `deploy/install.sh`<br>`admin/internal/upgrade/harden.go`<br>`admin/internal/upgrade/k3s.go` | `deploy/tests/preflight_test.sh#k3s kubeconfig is not world-readable`<br>`deploy/tests/preflight_test.sh#k3s: cifratura attiva dopo l'installazione`<br>`admin/internal/upgrade/harden_test.go#TestUpgrade_HardeningInstallazioneEsistente` |
| F3 | medium | Web tier security headers and TLS options (V9.1.2, V9.1.3, V14.1.3, V14.3.3, V14.4.3, V14.4.4, V14.4.5, V14.4.6, V14.4.7) | `web/deploy/nginx.conf.template`<br>`deploy/gitstack/templates/ingress.yaml`<br>`web/src/lib/nginxConf.test.ts` | `web/src/lib/nginxConf.test.ts#sets CSP, frame-ancestors, nosniff and Referrer-Policy on the SPA and hides the nginx version` *(new)*<br>`deploy/tests/preflight_test.sh#ingress sets HSTS and a TLS 1.2 minimum` *(new)* |
| F4 | **ok** | Cache and content-type headers on API responses (V8.2.1, V14.4.2) | `services/gateway/internal/httpserver/router.go`<br>`services/core/internal/httpserver/repos_code.go`<br>`services/git/internal/httpserver/code.go`<br>`services/gateway/internal/middleware/headers.go` | `services/gateway/internal/middleware/headers_test.go#TestAPIResponses_NoStoreNosniff` |
| F5 | medium | No request body size limit (V11.1.4) | `services/gateway/internal/proxy/proxy.go`<br>`services/core/internal/httpserver/resources.go`<br>`services/core/internal/httpserver/repos.go`<br>`services/git/internal/smarthttp/smarthttp.go` | `services/gateway/internal/httpserver/body_limit_test.go#TestBodyTooLarge_413` *(new)*<br>`services/git/internal/smarthttp/smarthttp_test.go#TestGzipBodyIsBounded` *(new)* |
| F6 | medium | **Resolved (GIT-232).** Rate limiting and abuse detection (V8.1.4, V11.1.4, V11.1.8) | `services/gateway/internal/middleware/ratelimit.go`<br>`services/gateway/internal/httpserver/router.go`<br>`deploy/gitstack/values.yaml` | `services/gateway/internal/middleware/ratelimit_test.go#TestRateLimit_PerTokenAndPerIP` |
| F7 | medium | **Resolved (GIT-233).** SSRF policy allowed the LAN and the node (V12.6.1) | `pkg/egress/policy.go`<br>`pkg/egress/README.md`<br>`services/core/internal/webhooks/egress.go`<br>`services/core/internal/webhooks/engine.go` | `pkg/egress/egress_test.go#TestNodeAddressesAreBlockedByDefault`<br>`services/core/internal/stackitest/m06_integration_test.go#TestM06E2E/webhook_to_node_address_blocked` |
| F8 | medium | Cluster isolation and in-cluster transport (V1.2.2, V1.9.1, V1.9.2, V1.14.1, V1.14.5, V9.2.1, V9.2.2) | `deploy/gitstack/templates/nats/statefulset.yaml`<br>`deploy/gitstack/templates/_helpers.tpl`<br>`deploy/gitstack/templates/gateway/deployment.yaml`<br>`deploy/gitstack/templates/core/deployment.yaml` | `deploy/tests/chart_hardening_test.sh#NetworkPolicy and securityContext are rendered` *(new)*<br>`deploy/tests/chart_hardening_test.sh#NATS requires credentials` *(new)* |
| F9 | medium | One static service secret for four services (V1.6.1, V2.10.1) | `deploy/gitstack/templates/identity/service-secret.yaml`<br>`services/gateway/internal/trust/trust.go`<br>`services/identity/internal/httpapi/tokens_keys.go` | `services/core/internal/trust/trust_test.go#TestVerify_SegretoDiUnAltroServizioRifiutato` *(new)* |
| F10 | medium | Security logging and audit trail (V1.2.3, V1.7.2, V7.1.3, V7.1.4, V7.2.1, V7.2.2, V7.4.3, V11.1.7) | `services/gateway/internal/middleware/logging.go`<br>`services/identity/internal/httpapi/httpapi.go`<br>`services/identity/internal/auth/auth.go`<br>`services/gateway/internal/httpserver/server.go` | `services/identity/internal/httpapi/httpapi_integration_test.go#TestAuthEventsAreLogged` *(new)*<br>`services/gateway/internal/middleware/logging_test.go#TestLogging_UserAndClientIP` *(new)*<br>`services/gateway/internal/middleware/recover_test.go#TestRecover_Generic500` *(new)* |
| F11 | medium | No multi-factor authentication (V2.3.2, V4.3.1) | `services/identity/internal/auth/auth.go`<br>`api/openapi.yaml` | `services/identity/internal/auth/auth_integration_test.go#TestLogin_SecondFactorRequired` *(new)* |
| F12 | low | Session lifetime and management (V3.3.2, V3.3.4) | `services/identity/internal/sessions/sessions.go`<br>`api/openapi.yaml` | `services/identity/internal/auth/auth_integration_test.go#TestSessionIdleTimeout` *(new)*<br>`services/identity/internal/httpapi/httpapi_integration_test.go#TestListAndRevokeOwnSessions` *(new)* |
| F13 | low | No notification on credential changes (V2.2.3, V2.3.3, V2.5.5) | `services/identity/internal/users/users.go`<br>`services/core/internal/mailer/mailer.go` | `services/core/internal/stackitest/m06_integration_test.go#TestM06E2E/credential_change_notifies_user` *(new)* |
| F14 | low | Password policy gaps (V2.1.2, V2.1.7, V2.1.8, V2.4.5) | `services/identity/internal/password/password.go`<br>`api/openapi.yaml`<br>`web/src/pages/ChangePasswordPage.tsx` | `services/identity/internal/password/password_test.go#TestValidatePolicy_CommonPasswordsRejected` *(new)*<br>`services/identity/internal/password/password_test.go#TestValidatePolicy_Max128` *(new)* |
| F15 | low | Content-Type not enforced on JSON bodies (V13.1.5, V13.2.5) | `services/core/internal/httpserver/resources.go`<br>`services/identity/internal/httpapi/httpapi.go`<br>`services/gateway/internal/httpserver/router.go` | `services/gateway/internal/httpserver/content_type_test.go#TestJSONBodyNeedsContentType_415` *(new)* |
| F16 | low | CSRF relies on SameSite=Lax only (V3.4.4, V4.2.2, V13.2.3) | `services/identity/internal/auth/auth.go`<br>`services/gateway/internal/middleware/auth.go` | `services/gateway/internal/middleware/csrf_test.go#TestCookieAuthRejectsForeignOrigin` *(new)* |
| F17 | low | Attachments: quota and malware scanning (V12.1.3, V12.4.2) | `services/core/internal/httpserver/attachments.go`<br>`services/core/internal/attachments/attachments.go` | `services/core/internal/httpserver/attachments_integration_test.go#TestAttachments_QuotaPerRepo` *(new)* |
| F18 | low | Git ingest limits and object checks (V12.1.1) | `services/git/internal/receiverules/receiverules.go`<br>`services/git/internal/mirrorpull/mirrorpull.go`<br>`services/git/internal/smarthttp/smarthttp.go` | `services/git/internal/smarthttp/receive_test.go#TestPushFsckRejectsMalformedObjects` *(new)*<br>`services/git/internal/mirrorpull/mirrorpull_test.go#TestPull_SizeCap` *(new)* |
| F19 | low | Process and release hygiene (V1.1.2, V14.2.2) | `.github/workflows/ci.yml`<br>`.github/workflows/security.yml`<br>`web/src/App.tsx`<br>`deploy/install.sh` | `scripts/check-actions-pinned.mjs#every uses: is pinned to a commit` *(new)*<br>`web/src/App.test.tsx#_components is not routed in production` *(new)* |
| F20 | low | Personal data handling (V6.1.1, V8.3.2) | `services/identity/internal/users/users.go`<br>`docs/operations.md` | `services/identity/internal/httpapi/httpapi_integration_test.go#TestExportOwnData` *(new)* |

### Details

**F1 (high) Personal token scope escalation.** `CreateToken` takes only the user id from the caller and validates the requested scopes against the catalogue, never against the scopes of the token that makes the call, although `createToken` in the contract says a token cannot have scopes its creator does not have. Anyone holding a token with only `write:user` (for example a leaked agent token) can mint a new token with `admin:org` and `write:resource` and keep it for up to 365 days (`DefaultMaxLifetime`). The same applies to `createUserToken` for an admin token. Found by reading the handler; not exploited at run time. Fix: reject, with 403 or 422, any scope the caller's token does not grant (`Grants` in `pkg/scopes/scopes.go`); sessions keep full scopes.

**F2 (high) Host and cluster secrets exposure.** The installer starts k3s with `--write-kubeconfig-mode 644` (`deploy/install.sh`, `k3s_env`), so `/etc/rancher/k3s/k3s.yaml` is readable by every local account, which is cluster-admin: any local user can read all Secrets (database password, service secret, webhook key, OIDC key, admin password) and the logs. The installer itself runs as root with `KUBECONFIG` set, so the mode 644 is not needed. k3s is also installed without `--secrets-encryption`. Precondition: a local, non-admin account on the server. Fix: mode 600 (or 640 with a group), enable secrets encryption, document it.

**Resolution (GIT-228).** k3s starts with `--write-kubeconfig-mode 600 --secrets-encryption` (`K3S_INSTALL_EXEC` in `deploy/install.sh`, `K3sInstallExec` in `admin/internal/upgrade/k3s.go`); the installer checks the kubeconfig mode and `k3s secrets-encrypt status` after the install. On existing installations `gitstack upgrade` (also with the same GitStack version) rewrites the k3s unit, restarts k3s, rotates the keys and re-encrypts the existing Secrets, with a preventive backup and a restore of the unit on failure: see "Hardening k3s (F2)" in `docs/operations.md`. The per-requirement rows above keep the outcome of the review of 2026-10-12; they point to F2, which is now resolved. Not covered: a vault or KMS for key material (V1.6.2, V6.4.2): the Secrets are encrypted at rest by k3s with a key on the same host.

**F3 (medium) Web tier security headers and TLS options.** The SPA and the API answer without `Content-Security-Policy`, `X-Frame-Options`/`frame-ancestors`, `X-Content-Type-Options`, `Referrer-Policy` and `Strict-Transport-Security`; only raw files, attachments and `/downloads` set `nosniff`. The page can be framed (clickjacking) and a single sanitiser bug becomes a script execution because no CSP is a second layer. nginx also sends its version, and no Traefik `TLSOption` pins the minimum TLS version or the cipher suites. Fix: add the headers in nginx (and HSTS on the HTTPS router), pin a `TLSOption`, hide the version; the CSP must be tested against the built SPA.

**F4 (medium) Cache and content-type headers on API responses.** JSON responses of core (issues, repositories, webhooks, notifications) and raw/archive responses carry no `Cache-Control`; only attachments (`private, no-store`) and identity responses do. Private data can stay in a browser or workstation cache after logout. API responses also lack `X-Content-Type-Options: nosniff`. Fix: a gateway middleware that adds `Cache-Control: no-store` and `nosniff` to every authenticated response.

**F5 (medium) No request body size limit.** Only issue bodies, attachments and a few identity and git handlers wrap the body in `http.MaxBytesReader`; the other core handlers (`resources.go`, `repos.go` and, through the generated wrappers, webhooks, labels, mirrors) decode an unbounded body into memory, and the gateway forwards bodies without any ceiling. The smart-HTTP handler also decompresses a gzip request body without a ceiling before it reaches `git`. A single authenticated client can exhaust memory. Fix: a global limit in the gateway and in core, with larger explicit limits where needed.

**F6 (medium, resolved by GIT-232) Rate limiting and abuse detection.** `middleware.RateLimit` is a no-op placeholder. Only login and password change are throttled, in memory per process. Nothing limits API calls per user, token or IP, nothing counts abnormal volumes or alerts, and the Git endpoints are served by the git service outside the gateway. Token guessing is not realistic (256 bits) but data scraping and denial of service are open. Fix: a real limiter per credential and per IP, with a counter that can be alerted on.

**Resolution (GIT-232).** `middleware.RateLimiter` (`services/gateway/internal/middleware/ratelimit.go`) is an in-memory token bucket per credential (token or session, keyed by SHA-256) and per client IP (`X-Forwarded-For` only from `gateway.env.trustedProxies`), thresholds in `gateway.env.rateLimit.*`; over the limit it answers 429 `rate_limited` with `Retry-After`, counts the rejection and logs `rate limit exceeded` with the obfuscated key. The limit is per gateway replica and does not cover the Git endpoints served outside the gateway (documented in `docs/operations.md`). Test: `ratelimit_test.go#TestRateLimit_PerTokenAndPerIP`.

**F7 (medium) SSRF policy allows the LAN and the node.** Loopback, link-local (metadata), cluster CIDRs and special ranges are always blocked, but RFC 1918 and the node's own LAN address are allowed unless the admin sets `egress.allow`/`egress.deny` (`TestCheckIPCategories` lists `10.0.0.1`, `172.16.5.5`, `192.168.1.10` as allowed on purpose, for CI servers on the LAN). A repository admin can therefore make core call kubelet (10250), the k3s API (6443), the router or any HTTP service on the LAN, over `http://`, and read up to 4 KB of the answer in the delivery log (`TestWebhooks_RispostaTroncataA4KB`). Mirror URLs are https only but the same range is reachable. Fix, a product decision: always block the node's own addresses and the Kubernetes API ports, and make the default allow list explicit during install (ask for the LAN ranges webhooks may reach).

**Resolution (GIT-233).** `pkg/egress` has `Config.NodeAddresses` (chart: `status.hostIP` as `GITSTACK_NODE_IP`, `egress.nodeAddresses`, and every IPv4 of the node written by `install.sh`): those addresses are blocked always, no `allow` entry unlocks them. The Kubernetes ports 6443, 10250, 10255 and 2379-2380 are refused on any private address (RFC 1918, ULA, CGNAT), at connect time and in the checks at creation. Webhooks and mirrors use the same policy (`webhooks.NewEgressForNode`, `mirrors.NewEgressWithNode`). `install.sh --egress-allow LIST` makes the reachable LAN ranges explicit. Not covered: with `egress.allow` empty, other LAN hosts on other ports remain reachable (CI servers live there); set `egress.allow` to close the list.

**F8 (medium) Cluster isolation and in-cluster transport.** The chart has no `NetworkPolicy`, so any pod (or anything running in the cluster) can reach identity, core, git, NATS and Postgres; NATS accepts connections without credentials or TLS, so an attacker pod can publish forged domain events (issue links, webhooks, notifications); the bundled Postgres DSN is `sslmode=disable`; service-to-service HTTP is cleartext. Pods set no `securityContext` (read-only root filesystem, dropped capabilities, `seccompProfile`, `allowPrivilegeEscalation: false`); images are non-root. Fix: default-deny NetworkPolicies with explicit allows, NATS user/password (or nkey), pod hardening, TLS for Postgres.

**F9 (medium) One static service secret for four services.** Gateway, identity, core and git all receive the same `identity-service` secret, used as bearer for `/internal/*` and as HMAC key to sign user identity headers (60 s window). The git service parses untrusted repositories and holds the key that can sign any identity, including the installation admin. The secret is generated once and never rotated, and there is no documented rotation procedure or key lifecycle. Fix: one secret per caller (or per pair), a documented rotation, and an identity header that git cannot mint for a user it did not authenticate.

**F10 (medium) Security logging and audit trail.** No event is logged for login success or failure, logout, password change, token or SSH key creation and revocation, grant changes or user deactivation; there is no audit table. The gateway request log has no user or token id and `remote_addr` is the proxy address, not the client. 403 and 401 decisions are only a status code. There is no recover middleware, so a panic closes the connection without a body or a structured line. Logs are not shipped or alerted. Fix: structured security events from identity and gateway (with actor, client IP and request id), a recover middleware, and a documented shipping path.

**F11 (medium) No multi-factor authentication.** Local accounts, including the installation admin, authenticate with a password only; no TOTP or FIDO second factor exists. OIDC users can get MFA from their provider. This is a product decision: either add a second factor for local accounts (at least for admins) or record an explicit exclusion in `docs/rules-coverage.md` with the OIDC recommendation for admins.

**F12 (low) Session lifetime and management.** Sessions expire only at an absolute 7 days (`DefaultTTL`), with no idle timeout, and users cannot list or revoke individual sessions (only the global revoke on password change). ASVS asks for a re-authentication after an idle period and for a session list. Fix: idle timeout (configurable), `GET/DELETE /user/sessions`.

**F13 (low) No notification on credential changes.** Password changes, admin password resets, new personal tokens, new SSH keys and token expiry produce no email or in-app notification, so a compromise goes unnoticed. Fix: a notification event for each, plus an expiry warning for tokens (also V2.3.3).

**F14 (low) Password policy gaps.** No breached or common password list is checked (V2.1.7), the web UI has no strength meter (V2.1.8), the maximum length is 1024 instead of 128 (V2.1.2) and no pepper is applied (V2.4.5). Hashing is argon2id and the minimum is 12, so exposure is limited. Fix: embed a list of common passwords, a meter in the UI and align the length limit.

**F15 (low) Content-Type not enforced on JSON bodies.** Handlers decode the body as JSON whatever the `Content-Type`, so a `text/plain` form post is parsed. SameSite=Lax prevents the cross-site use today, but the check is a cheap second layer. Fix: reject non-JSON on routes with a JSON body with 415.

**F16 (low) CSRF relies on SameSite=Lax only.** Cookie-authenticated state-changing requests are protected by `SameSite=Lax` only: there is no `Origin` or `Sec-Fetch-Site` check and the cookie has no `__Host-` prefix. Lax does not cover same-site subdomains or old browsers. Fix: reject cookie-authenticated unsafe methods whose `Origin` or `Sec-Fetch-Site` is foreign; rename the cookie with the prefix when served over HTTPS.

**F17 (low) Attachments: quota and malware scanning.** Each upload is limited to 10 MiB, but there is no count or total quota per user or repository and no antivirus hook; unlinked uploads are only purged after the orphan TTL. Fix: per-repo and per-user quota; document the antivirus decision (accept or hook).

**F18 (low) Git ingest limits and object checks.** `receive-pack` runs without `receive.fsckObjects`, pushes have a per-file limit (R6) but no total or per-repository ceiling, and a pull mirror fetch has only a timeout: an origin can fill the git volume. Fix: enable fsck, a size ceiling for mirror fetch and a per-repository quota alert.

**F19 (low) Process and release hygiene.** GitHub Actions are referenced by tag (`actions/checkout@v5`) and not by commit sha; `SECURITY.md` and the private reporting procedure (rule V4) do not exist; the component showcase `/_components` is routed in production; k3s is installed with `curl | sh` (pinned version, but script not pinned by checksum); the threat model has no trigger in the change process (V1.1.2). Fix: pin by sha, add SECURITY.md, gate the showcase to development builds, verify the k3s script checksum, add a threat-model checkbox to the item template.

**F20 (low) Personal data handling.** Names and emails are stored in clear in Postgres with no application-level encryption and no guidance on encrypting the Postgres volume and the local backup copy (S3 backups are encrypted, N7); there is no way for a user to export their data and no self-service removal (V6.1.1, V8.3.2). Impact is limited for an on-prem tool. Fix: operations guidance on disk encryption and a user data export.

## Out of the checklist

Things noticed that are not ASVS requirements: the go-test check of the work-item engine (`.galaxylab/checks.toml`) does not list `./pkg/scopes/...`, so the scope rule tests run in CI (`scripts/go-each.sh`) but not in that check; `SECURITY.md` does not exist yet (rule V4/V8).

