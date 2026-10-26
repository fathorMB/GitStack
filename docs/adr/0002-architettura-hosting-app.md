# ADR 0002 — v3-a: architettura dell'hosting delle app

- Stato: proposto (GIT-340), da approvare dal board
- Data: 2026-10-25
- Regole: H1–H13 [c_c4431e5184995e4f]; H5 [c_111f298013335dbc], H6 [c_42b11d7d8578bd37]
- Precedente: ADR 0001, controller CI dentro `core` [c_0e8ccf9288775e0e]
- Prova: [`poc-app-hosting/`](poc-app-hosting/)

## Contesto

v3-a ospita app interne, senza disco, appartenenti a un repository. `read` vede stato e
storico; `write` distribuisce e legge log; `admin` configura e rimuove [c_111f298013335dbc].
L'indirizzo automatico usa la CA interna e l'accesso passa dal login GitStack
[c_e98c52d28804ed6c]. Vale la regola «pochi servizi»: niente componente nuovo se `core` basta.

## Decisioni, alternative e rischi

### Controller in core

Il reconciler `apps` vive in `services/core/internal/apps`: legge il modello Postgres, crea e
osserva Deployment, Service e IngressRoute con il client Kubernetes e pubblica stato/eventi
con l'outbox di `core`. È una goroutine con coda, timeout e retry idempotenti, non un servizio.

Un controller/deployment nuovo separerebbe il privilegio Kubernetes, ma aggiungerebbe API,
credenziali e operatività; rimane possibile come secondo deployment dello stesso binario se
l'RBAC di `core` cresce troppo. Gateway e git sono scartati: non possiedono modello né eventi.

Rischio: un client Kubernetes bloccato non deve bloccare HTTP; worker e timeout sono separati.

### Namespace, ServiceAccount e RBAC

Ogni app ha namespace `gitstack-app-<app-id>`, ServiceAccount senza token montato e Role/
RoleBinding limitati al namespace. Il controller riceve un RoleBinding per namespace solo per
Deployment, Service, Secret, ConfigMap, IngressRoute, Pod e `pods/log` dell'app; non esiste un
ClusterRole per le app. Per creare e cancellare i namespace, `core` ha invece un ClusterRole
strettamente del controller: `create`, `get`, `list`, `watch`, `delete` su `namespaces`; `get`,
`list`, `watch`, `create`, `delete` su `rolebindings`; e `bind` solo sui Role `gitstack-app-*`.
Il reconciler agisce esclusivamente su namespace con label `gitstack.io/managed-app=true` e
verifica app-id/owner prima di ogni mutazione. Il namespace applica Pod Security `restricted`, default-deny
NetworkPolicy, ResourceQuota e LimitRange. Root nell'app è consentito da H9, mai privilegi
Kubernetes, host namespace, hostPath o capability.

Un namespace per repo impedisce quota, policy, identità e rimozione indipendenti; uno condiviso
 rende un errore RBAC laterale: entrambi scartati. Rischi: più namespace/RoleBinding e policy di
egress da esplicitare dall'admin; capacità e naming vanno provati con molte app.
Il ClusterRole di `core` resta un privilegio critico: una label falsificata o un bug nel selector
può allargare il perimetro; il chart deve impedirne la scrittura ai ServiceAccount delle app.

### Esposizione e TLS

Ogni app interna usa `<app>-<repo>-<owner>.apps.<dominio-istanza>` (per esempio
`web-handbook-acme.apps.acme.it`). Owner, repo e app sono normalizzati per DNS e la label
deve restare entro 63 caratteri: altrimenti l'API rifiuta con `address_too_long`. Un indice unico
su `apps.normalized_host` rifiuta creazione o rinomina ambigua con `address_conflict`: app/repo
con trattini possono infatti produrre la stessa label. La forma evita solo le collisioni fra repo
omonimi di owner diversi. Il controller crea una
`IngressRoute` Traefik `websecure` verso il Service; chart e installer distribuiscono un
wildcard `*.apps.<dominio-istanza>` firmato dalla CA interna. Il Secret TLS resta solo nel
namespace Traefik, come `TLSStore` `default`; le IngressRoute dichiarano `tls: {}` e non ne
ricevono una copia. La chiave CA resta sull'host, come nel TLS esistente. La prova k3d esercita
esattamente questo percorso.

Ingress standard è scartato: Traefik già governa middleware e routing GitStack. Un certificato
per app è scartato per costo di rinnovo; la copia del wildcard in ogni namespace è scartata perché
moltiplica la chiave privata. Domini propri/Let's Encrypt sono v3-c. Rischi: DNS wildcard e
distribuzione CA sono prerequisiti; il TLSStore default richiede che nessun'altra rotta possa
inavvertitamente usare quel certificato.

### Login H6 e intestazioni firmate

Traefik usa `Middleware.forwardAuth` verso il **gateway**. Gateway verifica sessione o Bearer,
controlla `read` sul repo in `core` e risponde 2xx solo se autorizzato. Passano solo
`X-GitStack-User`, `X-GitStack-Email`, `X-GitStack-Teams`, `X-GitStack-Auth-Time` e
`X-GitStack-Signature`: HMAC-SHA-256 su metodo, path, host, timestamp e valori canonici; firma
con versione e key id, chiave per app in Secret. Un Middleware Traefik `headers` con
`customRequestHeaders` vuoti rimuove tutti gli `X-GitStack-*` dal client ed è ordinato prima del
forward-auth; soltanto le intestazioni restituite dal gateway arrivano all'app.

Realizzazione (GIT-347, [`../apps-login.md`](../apps-login.md)). Scostamenti dal testo sopra: la firma è
**Ed25519** con chiave pubblica pubblicata dal gateway (`/.well-known/gitstack-app-keys`), non HMAC: un
HMAC obbligherebbe a dare all'app il segreto che permette di fabbricare le intestazioni. Le chiavi sono
del gateway (elenco "nuova,vecchia" per la rotazione), non per app. Il forward-auth è per app: core crea
`gitstack-strip` e `gitstack-auth` nel namespace dell'app, e l'indirizzo del secondo porta app, repo e una
firma HMAC con il segreto del gateway. Il cookie di sessione non arriva all'app perché il gateway
restituisce `Cookie`/`Authorization` ripuliti in `authResponseHeaders`. Il cookie GitStack è dell'host
di GitStack e non viaggia verso l'host dell'app: il passaggio dal browser usa un codice di un minuto e un
cookie d'app (`gst_app_session`). La regola di accesso sta in una sola funzione (`AccessPolicy`).

Un proxy dedicato duplicherebbe sessioni e policy; header non firmati o token inoltrato lasciano
spoofing/riuso: scartati. Rischi: TTL breve, rotazione con doppia chiave e verifica WebSocket/
streaming sono necessari; un header già consegnato non è revocabile subito.

### Salute, versione e rollback

Ogni revisione è un Deployment con readiness/liveness probe, `progressDeadlineSeconds` e
immagine per digest. Il reconciler considera riuscito solo lo status con repliche aggiornate e
available, registra digest/manifest/timestamp e, al deadline, scala giù la nuova revisione e
riporta il Service alla precedente pronta. Il rollback manuale usa lo stesso percorso.

Una sonda solo nel controller duplica Kubernetes; lasciare rollback solo manuale non soddisfa
H12: scartati. Rischi: readiness può essere superficiale; canary e controllo HTTP esterno sono
futuri. Le migrazioni incompatibili restano fuori: v3-a non ha dati persistenti.

### Log, metriche e cronologia

`core` legge `pods/log` e applica `write`; il browser non parla con Kubernetes. Interroga
metrics-server e status pod per CPU, RAM e riavvii; Traefik fornisce richieste/errori per route.
Postgres conserva indice, eventi, stato e campioni; i log compressi stanno su `apps-data`, 7
giorni o 1 GiB per app, regolabili, con pulizia.

Log grezzi in Postgres sono scartati per volume/backup; solo nel pod li perderebbe al riavvio.
Loki/Prometheus è un backend futuro, non un servizio iniziale. Rischi: metrics-server può
mancare (l'API dichiara indisponibile, non zero); log e quote entrano nel piano backup.

### Priorità e risorse

Si riusano le classi ADR 0001: `gitstack-services` (1 000 000) > `gitstack-apps` (100 000) >
`gitstack-ci-job` (1 000). Le app usano `gitstack-apps`; quota per owner in `core` riserva e
applica tetti CPU/RAM/repliche, ResourceQuota applica il limite nel namespace. Le app possono
preemptare solo CI.

Priorità uguale a CI non rispetta H11; una classe per repo non governa quote per owner: scartate.
Rischio: riserva non è capacità fisica; installer e board devono fissare preflight e default.

### Firme al deploy H10

Prima di ogni Deployment il reconciler esegue `cosign verify --key` sul digest con chiave
pubblica del registry dell'istanza e identità fissata; salva digest ed esito. Esito assente,
scaduto o invalido blocca il deploy. Usa cosign pinnato nell'immagine core o libreria equivalente.

Admission controller difenderebbe anche `kubectl`, ma aggiunge un componente critico prima che
v3-a abbia altri writer: rinviato come difesa aggiuntiva. Immagini non firmate sono escluse.
Rischi: un admin Kubernetes può aggirare il controller finché manca l'admission policy; caching
bundle deve gestire registry offline senza accettare risultati stantii.

## Moduli per gli item successivi

| Modulo | Responsabilità |
|---|---|
| `services/core/internal/apps/model` | tabelle, migrazioni, stato e autorizzazione repo |
| `services/core/internal/apps/reconcile` | coda, client Kubernetes, risorse e rollback |
| `services/core/internal/apps/registry` | verifica cosign e immagini incluse nel backup |
| `services/core/internal/apps/observability` | log, metrics-server, retention e campioni |
| `services/gateway/internal/appauth` | forward-auth, autorizzazione e header HMAC |
| `deploy/gitstack/templates/apps-*` | namespace, RBAC, policy, quote, classi e TLS |
| `api/openapi.yaml`, `cli`, `web` | API Apps, `gs app`, UI; stessa autorizzazione core |

Ogni item che introduce schema avrà migrazione e test; ogni modulo Kubernetes avrà prova k3d.

## Prova di fattibilità

[`poc-app-hosting/run.sh`](poc-app-hosting/run.sh) crea k3d, CA privata, namespace,
`IngressRoute` TLS e forward-auth. Verifica richiesta HTTPS contro la CA e header utente ricevuto
dall'app, poi `401` della route negata. È prova del percorso scelto, non della sessione GitStack
reale: il gateway lo implementerà negli item successivi.

## Rischi trasversali aperti

La prova non dimostra sessioni reali, rotazione HMAC, metrics-server, cosign reale o capacità con
molte app. Queste verifiche sono criteri degli item di implementazione, non assunzioni implicite.
