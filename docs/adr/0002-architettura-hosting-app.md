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

### Nomi interni fra app dello stesso repo (H15, GIT-379)

Aggiornamento del 2026-10-26 (approvato dal CTO con la modifica sulle porte) per il Pilota retail.it [c_ec914aebbbb8b274]: dentro lo stesso repo
ogni app si raggiunge con il proprio nome, per esempio `http://api` dal sito, stabile tra
deploy, rollback, riavvio e scala. Oggi ogni app ha il suo namespace e un Service `app` (`WorkloadName`):
il nome `api` non risolve da un altro namespace.

**Meccanismo scelto: un Service `ExternalName` per ogni app sorella, nel namespace di ciascuna app.**
Per l'app A del repo R, il reconciler crea nel namespace di A, per ogni altra app B di R, un Service
`<nome di B>` di tipo `ExternalName` con `externalName: app.gitstack-app-<id di B>.svc.cluster.local`
(il Service `app` di B). Da A, `http://api` risolve con un CNAME verso il Service di B. Dettagli:

- **Stabilità.** L'alias punta al Service `app` di B, che il controller non ricrea a ogni deploy:
  cambia solo la revisione dietro il selector. Deploy, rollback automatico e manuale, riavvio e
  scala non toccano né l'alias né il Service. Non ci sono IP da aggiornare (nessun Endpoints a mano).
- **Porte (decisione del CTO, 26/10).** `ExternalName` non rimappa le porte: il client usa quelle del
  Service `app` di B. Per questo il Service `app` espone DUE porte: la **80** (nome `http`, targetPort
  `http`, come oggi: la usano IngressRoute e forward-auth) e, se diversa da 80, la **porta dichiarata**
  dall'app (nome `app`, targetPort uguale al numero). Funzionano `http://api`, `http://api:<porta
  dichiarata>` e `postgres:5432` (il caso che ha generato H15: l'api che raggiunge il database), senza
  Endpoints a mano. Cambiando la porta dell'app il Service si riallinea al deploy. L'app con `Port`
  vuota non ha Service e non viene pubblicata. La NetworkPolicy non limita le porte in ingresso fra app
  dello stesso repo: la porta dichiarata passa.
- **Perimetro.** Gli alias sono creati solo fra app con lo stesso `repo_id`, mai altrove: un'app di
  un altro repo con lo stesso nome non ha alcun alias nel namespace e non risolve. Il nome inoltre è
  una singola label nel namespace: non esiste un DNS globale. Anche con un nome indovinato
  (`app.gitstack-app-<id>.svc`) la NetworkPolicy `app-isolation` di GIT-344 (ingress ed egress
  solo verso namespace e pod con lo stesso `gitstack.io/repo-id`) blocca il traffico: l'alias è
  comodità di nome, non un permesso. Le due barriere sono indipendenti.
- **DNS.** La risoluzione richiede che il pod possa interrogare il DNS del cluster. `BuildNetworkPolicy`
  (`app-isolation`, l'unica policy per namespace, GIT-344) aggiunge sempre la regola UDP/TCP 53 verso
  `kube-system` `k8s-app=kube-dns` (prima solo con un proxy con nome) e nient'altro. Il nome interno non
  apre quindi nessuna uscita verso internet.
- **Aggiornamento.** L'insieme degli alias è derivato dallo stato: a ogni `Sync`/deploy il reconciler
  calcola le app del repo con Service e fa create/update degli alias mancanti o cambiati e
  cancella gli alias con label `gitstack.io/sibling-alias=true` non più voluti. Creazione di B: alias
  aggiunto a tutte le sorelle e alias delle sorelle in B; rinomina: vecchio alias tolto, nuovo creato;
  eliminazione: alias tolto da tutte le sorelle (con il namespace di B). Un'app rimossa o in errore
  lascia al massimo un alias che non risponde per un ciclo di Sync. Il reconciler tocca solo Service
  con la sua label, e solo in namespace `managed-app` dello stesso repo.
- **RBAC e ammissione.** Il ClusterRole modello ha già `create/get/update/delete` sui Service del namespace
  dell'app; si aggiunge `list`, per togliere gli alias non più voluti. La ValidatingAdmissionPolicy
  `gitstack-apps-controller-scope` copre anche i Service del solo ServiceAccount di core: solo in
  namespace `gitstack-app-*` marcati, e un `ExternalName` ammesso solo se è
  `app.gitstack-app-<uuid>.svc.cluster.local` (il Service `app` di un'altra app), mai un nome esterno.
- **Collisioni.** Un alias non è creato per l'app stessa (A usa già `app`/localhost) né quando il nome
  coincide con `app`, `kubernetes` o altro nome già occupato nel namespace (Service o altro): è
  saltato con un evento di avviso, non è un errore del deploy. Il nome deve essere una label DNS-1035
  valida dopo la normalizzazione di `pkg/names`; altrimenti l'app non è pubblicata (l'host esterno
  resta). Un'app non raggiunge sé stessa per nome.
- **Documentazione.** `docs/apps.md`, sezione «Raggiungere un'altra app del repo per nome (H15)».

Alternative scartate: (1) un Service con Endpoints a mano: IP dei pod che cambiano a ogni deploy,
da riallineare; (2) un Service ExternalName verso l'host esterno `<app>-<repo>-<owner>.apps…`:
passa dal forward-auth e dal TLS, non è «interno» e la NetworkPolicy lo bloccherebbe; (3) un
namespace condiviso per repo con Service `<app>`: risolve il nome ma annulla l'isolamento per
app (quota, policy, RBAC, rimozione) già scelto sopra; (4) CoreDNS con una zona per repo: modifica
una risorsa di sistema condivisa e richiede un privilegio di cluster in più per `core`; (5) un
proxy/mesh: componente nuovo, contro la regola «pochi servizi».
Rischi: più Service per namespace (n·(n−1) per repo, con poche app per repo); un `ExternalName`
è un CNAME, quindi il tempo di cambio è quello del TTL DNS del cluster; `ExternalName` richiede
RBAC `create/update/delete services` che il Role del controller già ha per gli stessi Service; gli
header `Host` verso l'app sono quelli scritti dal chiamante (`api`), l'app non deve dipenderne.
Prova: `TestApps_NomiInterniSuK3d` (`services/core/internal/httpserver/apps_names_integration_test.go`,
lanciato da `services/core/internal/apps/k3d-test.sh`), una riga ok/ko per controllo: `api` raggiunta
dal pod di `site` su `http://api:8080` e `http://api`, anche dopo un nuovo deploy e un rollback di
`api`; app `api` di un altro repo né risolta né raggiungibile.

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

## App pubbliche e domini propri (v3-c)

Decisioni di contratto (GIT-455); `api/openapi.yaml` ne fissa le forme (`/repos/{owner}/{repo}/apps/{app}/domains`,
`AppsInstanceSettings.letsEncrypt`), l'implementazione arriva negli item successivi di v3-c.
Un'app è raggiungibile da internet solo con il doppio consenso: l'amministratore ha abilitato le app
pubbliche (`publicAppsEnabled`) e chi ha `admin` sull'app ha scelto l'accesso `public`.

### Controllo del DNS

Ogni dominio proprio nasce `pending_dns`. `core` risolve il dominio con il resolver di sistema
(lookup diretto, senza cache propria) e confronta con il record atteso, che l'API restituisce in `dns`:

- **CNAME** verso l'indirizzo automatico dell'app (`<app>-<repo>-<owner>.apps.<dominio-istanza>`): vale se
  la catena di CNAME termina lì. È la forma consigliata per i sotto-domini (`shop.retail.it`).
- **A/AAAA** verso uno degli IP pubblici dichiarati dall'amministratore (`publicIps`): vale se
  l'insieme degli indirizzi risolti è contenuto in quelli dichiarati, senza altri IP. È l'unica forma per
  un dominio all'apice, dove il CNAME non è ammesso. Senza IP dichiarati la forma A/AAAA non è proposta.

Esiti: tutto coincide → `verified`; nessun record (NXDOMAIN, nessuna risposta, timeout) → resta
`pending_dns`; record presenti ma diversi → `error` con `statusReason` (per esempio l'IP trovato). Un errore
del resolver locale che non è una risposta DNS del dominio (per esempio timeout, SERVFAIL o errore di rete) non
modifica lo stato né `failing_since` di un dominio `verified` e viene registrato nel log; per `pending_dns` resta
`pending_dns`. NXDOMAIN e record diversi seguono invece le regole sotto. Il controllo ripete a intervalli (ogni 5 minuti,
poi più radi) e su richiesta con `POST .../recheck`; un dominio `verified` con record non coincidenti resta in grazia.
Lo stato non cancella mai dati.
La route Traefik del dominio e l'emissione del certificato partono solo da `verified`.

Realizzazione (GIT-458): il controllo è in `services/core/internal/apps/domains.go` (resolver configurabile, policy di
`pkg/egress` per segnalare i risultati su indirizzi interni) e gira alla richiesta e ogni minuto dal controller. Ritmo:
`pending_dns` ed `error` ogni 5 minuti nella prima ora, ogni 30 minuti fino al giorno, poi ogni 6 ore; `verified` ogni 10 minuti
(5 durante la grazia). **Periodo di grazia**: un `verified` il cui DNS non coincide più (anche se sparisce) resta `verified`
e con la route per 1 ora (`failing_since`); scaduta passa a `error` col motivo e perde la route. Gli IP pubblici dichiarati sono
in `core.app_capacity_settings.public_ips` (la scrittura arriva con le impostazioni ACME). Senza lista dei suffissi pubblici,
«apice» vuol dire un nome di due etichette. Gli host dei domini `verified` sono aggiunti alla route dell'app da
`domainHosts` (`controller.go`, `ensureRoute`), solo per le app `public` a interruttore acceso; togliere un dominio riscrive
subito la route.
Domini non ammessi: il dominio di GitStack, il dominio delle app e i suoi sotto-domini, gli indirizzi IP
(`domain_reserved`); un dominio già legato a un'altra app o a questa (`domain_taken`, indice unico sul
dominio normalizzato, minuscolo e senza punto finale); un'app che non ha accesso `public` non ha domini
(`app_not_public`).

### Certificato

- **Indirizzo automatico di un'app pubblica**: certificato Let's Encrypt emesso per quell'host
  (HTTP-01 sulla porta 80, via Traefik), non la CA interna di H2 che nessun visitatore da internet si
  fida. Vale solo per le app con accesso `public`; le altre mantengono il certificato wildcard della CA interna.
  `AppAccess.certificate` vale `lets_encrypt` per l'app pubblica e `instance_ca` per le altre. Se Let's Encrypt
  non è utilizzabile (nessuna email ACME o porta 80 non raggiungibile), l'app pubblica resta servita col
  certificato wildcard della CA interna, con avviso (`AppsLetsEncryptStatus`), invece di restare senza HTTPS.
- **Domini propri**: per default `letsencrypt` (emissione e rinnovo automatici dopo `verified`, certificato
  `none → issuing → valid | error`). Il cliente può caricare il proprio certificato con
  `PUT .../certificate` (modalità `custom`); il rinnovo diventa suo. Il caricamento valida PEM, chiave
  corrispondente, copertura del dominio e scadenza (`certificate_invalid`); la chiave non si rilegge.
  `DELETE .../certificate` torna a `letsencrypt`.
- **Stato ACME d'istanza**: email dell'account, IP pubblici dichiarati, porta 80 raggiungibile o no (con
  data e motivo dell'ultimo controllo). Senza email ACME Let's Encrypt è spento.

### Disabilitare le app pubbliche

Quando l'amministratore porta `publicAppsEnabled` a `false`, le app con accesso `public` non sono più
raggiungibili da internet: le loro route (indirizzo automatico e domini propri) sono tolte, senza
fermare né riavviare l'app, che resta raggiungibile dentro l'installazione col login GitStack (H6)
come `login`. La scelta `public` di ogni app, i domini propri, i certificati caricati e quelli emessi **sono
conservati**: riabilitando le app pubbliche le route tornano come prima, senza che nessuno debba rifare
la scelta. Nel frattempo `GET .../access` mostra `mode: public` con `publicAppsEnabled: false` e l'indirizzo
non esposto; `setAppAccess` con `public` risponde 422 `public_apps_disabled` e i domini non si aggiungono
(`app_not_public`) né si ricontrollano. Il rinnovo ACME è sospeso e riprende alla riabilitazione.


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
