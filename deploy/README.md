# deploy/

Tutto ciò che serve a far girare GitStack su k3s: manifest/Helm chart interno dei servizi (`gateway`, `identity`, `git`, `core`, `web`, `postgres`, `nats`), lo script di installazione a comando singolo e la VM di test riproducibile. Architettura: `docs/operations.md` e la knowledge base interna in `.prisma/knowledge/`.

- `postgres-prepare.sql` — script per il DBA: database e utente per un Postgres esterno (N8).
- `gitstack/` — chart Helm interno (M-01/T-08): installa `gateway`, `identity`, `core`, `web`, `postgres` e `nats` (JetStream) con un solo `helm install`, Traefik come ingresso. Dettagli, valori di default e opzioni (mirror, Postgres esterno) in `gitstack/README.md`. `identity` (GIT-36) è nel chart, con ruolo e schema Postgres dedicati e le sue rotte instradate dal gateway; `git` non ha ancora un'immagine: non è nel chart.
- `install.sh` — Installer v0 (M-01/T-09, questo item): un solo comando installa k3s e GitStack (chart di `gitstack/`) su una macchina Linux pulita. Dettagli sotto.
- `ci/install-e2e.sh` — passi del workflow `.github/workflows/install.yml` (GIT-203, GIT-285): installazione, backup, aggiornamento con cambio di patch di k3s, rollback e ripristino su Ubuntu 22.04 e 24.04, con la CI integrata accesa e un workflow di prova eseguito in un pod, sui tag, ogni notte su main e a mano. Vedi `../docs/operations.md`, «CI tests of install, upgrade and restore».
- `test-vm/` — script che creano, ripristinano e distruggono la VM Linux locale usata per provare l'installer (M-01/T-12, GIT-12). Vedi `test-vm/README.md`.

Licenza: AGPL-3.0, come il resto del server (vedi LICENSE in radice).

## Installer v0 (`install.sh`)

Un solo comando installa k3s e GitStack su una macchina Linux pulita. Il cliente non deve sapere cos'è Kubernetes.

```sh
# Da un checkout del monorepo (usa il chart accanto allo script):
sudo ./deploy/install.sh

# Senza checkout locale (scarica da solo deploy/gitstack dal repository sorgente):
curl -fsSL https://raw.githubusercontent.com/fathorMB/GitStack/main/deploy/install.sh | sudo bash
```

### Distribuzioni supportate

Nella v1.0 sono ufficialmente supportati solo **Ubuntu Server 22.04 e 24.04 LTS**, amd64: su questi il preflight passa senza avvisi e l'installazione gira in CI (N2 rivista il 2026-10-18, [c_d345922b89557a3f]).

**Pop!_OS 22.04 e 24.04** (amd64) si installa senza `--force`: il preflight stampa un avviso («non è supportato ufficialmente nella v1.0», in italiano e in inglese) e prosegue, senza garanzia e senza test in CI.

Gli altri sistemi restano bloccati: il preflight stampa «non supportato» e si ferma, si procede solo con `--force`, a proprio rischio. `--force` non salta i requisiti hardware.

### Differenze fra i sistemi supportati

L'installer installa k3s e i servizi allo stesso modo su tutti e quattro i sistemi. Di seguito le differenze che l'operatore o l'installer devono gestire.

**cgroup v2.** Ubuntu 22.04 usa cgroup v2 di default; Ubuntu 24.04 e Pop!_OS 24.04 lo danno per scontato. Verificare che `/sys/fs/cgroup/cgroup.controllers` esista: è un requisito di k3s, non controllato dall'installer.

**iptables/nftables.** Dal 21.04 Ubuntu usa `iptables-nft` come alternativa di default (iptables-nft è un wrapper verso nftables, non `iptables-legacy`). k3s usa il proprio iptables interno o quello di sistema, e funziona con entrambe le versioni. Nessun intervento dell'installer necessario.

**systemd-resolved.** Su Ubuntu 22.04 e 24.04 il resolver systemd (`systemd-resolved`) gestisce la risoluzione DNS; su Pop!_OS la configurazione è analoga. Se si usa `--host` con un nome DNS, verificare la risoluzione con `resolvectl query <nome>`. I pod di k3s usano il DNS del cluster, non systemd-resolved.

**NetworkManager e firewall su Pop!_OS.** Pop!_OS usa NetworkManager per gestire le interfacce di rete. k3s crea le interfacce `cni0` e `flannel.*` durante l'installazione: per evitare che NetworkManager le gestisca, aggiungere la configurazione `/etc/NetworkManager/conf.d/10-k3s.conf` (o un file in `/etc/NetworkManager/conf.d/` con estensione `.conf`) con:

```ini
[keyfile]
unmanaged-devices=interface-name:cni0;interface-name:flannel.*;interface-name:flannel.1
```

Pop!_OS include anche `ufw` (uncomplicated firewall) disabilitato di default; se attivo, aprire le porte 80, 443, 6443 e la porta SSH di git (default 2222). L'installer non modifica ufw.

**AppArmor.** Le distribuzioni basate su Ubuntu includono AppArmor, di default in modalità complain per i profili di sistema. Non dovrebbe bloccare i servizi k3s, ma su configurazioni harden verificare con `aa-status` che non ci siano profili in enforce che interferiscano.

**Comando `gitstack` e timer di backup.** Entrambi funzionano identicamente su tutti e quattro i sistemi (systemd è presente su tutti). Il comando `gitstack` è installato in `/usr/local/bin/gitstack` (visibile con `sudo gitstack status`). Il timer di backup (`gitstack-backup.timer`) è installato da `install.sh` in `enable_backup_timer`: crea il file unit in `/etc/systemd/system/gitstack-backup.timer` e lancia `systemctl enable --now gitstack-backup.timer`, invocando `/usr/local/bin/gitstack backup` all'orario configurato (default alle 02:00, variabile `GITSTACK_BACKUP_TIMER_HOUR`).

### Requisiti: due profili (N1)

Lo script li verifica in preflight prima di installare qualunque cosa (N1, confermati il 2026-10-05 [c_d345922b89557a3f]):

| Requisito | Profilo minimo: si ferma | Profilo consigliato: solo avviso |
|---|---|---|
| CPU | 4 vCPU | 8 vCPU |
| RAM | 8 GB | 16 GB |
| Disco | filesystem di almeno 60 GB **e** almeno 20 GB liberi, sul filesystem che ospita `/var/lib/rancher` (se non esiste ancora, `/`) | 200 GB su SSD (fino a circa 100 utenti) |
| Sistema | Ubuntu Server 22.04/24.04 amd64 (supportato nella v1.0); Pop!_OS 22.04/24.04 amd64 con avviso nel preflight; altri sistemi solo con `--force` | |
| Accesso | root o sudo | |
| Rete | uscita verso `get.k3s.io`, `get.helm.sh` e il registry delle immagini (`ghcr.io` di default, configurabile; l'air-gapped arriva con M-08) | |
| Porte libere | 80, 443, 6443 e la porta SSH di git (già occupate da un'installazione GitStack esistente non sono un errore: vedi Idempotenza) | |

Sotto il minimo l'installer esce con exit 1 e il valore trovato; sotto il consigliato stampa `ATTENZIONE` e prosegue. L'SSD si legge da `/sys/class/block/<dev>/queue/rotational` (segue partizioni e device mapper); se non si capisce, avviso (un disco virtuale può dichiararsi rotazionale: è solo un avviso, come sulla VM di test). Lo spazio per i repo è circa il doppio della loro dimensione: sui dischi piccoli pesa il consigliato.

Tutti i GB sono decimali (10⁹ byte), non GiB: una macchina con «8 GB» di RAM riporta qualche centinaio di MiB in meno in `/proc/meminfo` (memoria riservata a firmware/hypervisor), e un disco da 60 GiB dichiara oltre 64 GB, quindi i margini di partizionamento non fanno fallire la macchina di riferimento (la VM di GIT-12). Dimensione del filesystem e spazio libero sono due controlli distinti: lo spazio libero cala dopo k3s e le immagini, e il preflight gira a ogni avvio.

Per saltare tutti i controlli (solo su una macchina già verificata a mano): `--skip-preflight` o `GITSTACK_SKIP_PREFLIGHT=1`. Non è mai attivo di default.

### Nome host (N6)

`--host NOME|IP` (ripetibile) dice con quale nome i client raggiungono GitStack: il primo è nell'URL pubblico (clone, link nelle email, OIDC), tutti finiscono nei SAN del certificato, che include sempre anche l'IP principale. Senza `--host` si usa il nome completo della macchina (`hostname -f`) se risolve a un suo indirizzo, altrimenti l'IP, con un avviso.

Il preflight verifica che il nome risolva a un indirizzo della macchina e **avvisa** (senza fermarsi) se no: nome che non risolve, che risolve altrove o solo a loopback. La verifica passa dal resolver di sistema (`getent hosts`), non da un server DNS, quindi vale anche per i nomi `.local`.

Per cambiare nome dopo l'installazione: `sudo gitstack config set host <nome>`. Rigenera il certificato (`gitstack-tls ensure`, stessa CA, SAN col nuovo nome e gli IP già presenti), aggiorna gli URL pubblici dei servizi con `helm upgrade --reuse-values` sulla copia locale del chart (`/usr/local/share/gitstack/chart`, `chart_dir` in `config.yaml`), poi `host:` di `config.yaml` e le scelte salvate per le riesecuzioni di `install.sh`. Avvisa che gli indirizzi di clone e i link nelle email già inviate puntano al nome vecchio (`git remote set-url` per i clone esistenti). Per Let's Encrypt il nome deve essere pubblico; con `--insecure-http` non c'è certificato.

### Dominio delle app (H2, GIT-346)

Le app che GitStack ospiterà (v3) hanno un indirizzo automatico `<app>-<repo>-<owner>.<dominio-delle-app>`, per esempio `web-handbook-acme.apps.acme.it` (ADR 0002). Il dominio si sceglie all'installazione:

```bash
sudo ./deploy/install.sh --host git.azienda.it --apps-domain apps.azienda.it
```

- **`--apps-domain DOMINIO` è facoltativo.** Senza, l'installer usa `apps.<host>` (con un `--host` che è un IP: `apps.gitstack.internal`) e **avvisa** che il record DNS wildcard va creato. Una riesecuzione dell'installer conserva la scelta fatta (`/etc/gitstack/tls/install.conf`, `apps_domain:` in `config.yaml`).
- **Consigliato un dominio separato** da quello di GitStack (`git.azienda.it` e `apps.azienda.it`): le app non possono oscurare i nomi di GitStack. È ammesso anche un suo sottodominio (`apps.git.azienda.it`); il dominio uguale all'host è rifiutato, e un host dentro il dominio delle app dà un avviso. Regole: nome DNS in minuscolo, almeno due etichette, solo lettere, cifre e `-`.
- **DNS wildcard da configurare.** Serve un record `*.<dominio-delle-app>` che punta all'IP di GitStack, per esempio `*.apps.azienda.it. A 192.168.1.20` (o `CNAME` verso il nome dell'host), oppure `address=/apps.azienda.it/192.168.1.20` in dnsmasq. Il preflight prova `getent hosts gitstack-dns-check.<dominio>` e avvisa, senza fermarsi, se non risolve a questa macchina. Un dominio `.local` non va bene: mDNS non ha record wildcard.
- **Certificato wildcard.** Con la CA interna (default) `gitstack-tls` aggiunge ai SAN del certificato del server `*.<dominio-delle-app>`: la stessa CA e lo stesso rinnovo notturno (`gitstack-tls-renew.timer`) del certificato di GitStack, nessuna CA da installare in più sui client. Un wildcard copre un solo livello (`web-handbook-acme.apps.acme.it`, non `a.b.apps.acme.it`). Con il certificato del cliente (`--tls-cert`) il certificato deve già coprire `*.<dominio>`: altrimenti `gitstack-tls` avvisa. Con `--tls letsencrypt` l'indirizzo è registrato ma il wildcard della CA interna non viene emesso.
- **Indirizzo e collisioni.** `<app>-<repo>-<owner>` è una sola etichetta DNS (massimo 63 caratteri): owner e repository passano in minuscolo, con `_`, `.` e altri caratteri trasformati in `-`. L'owner nell'etichetta distingue due repo omonimi di owner diversi (`web-handbook-acme`, `web-handbook-globex`). Non ci sono suffissi automatici: oltre i 63 caratteri l'API rifiuta con `address_too_long`, e un'etichetta già in uso (`a-b` in `c` contro `a` in `b-c`, stesso owner) con `address_conflict`, garantito dall'indice unico su `apps.normalized_host`. Le regole sono in `pkg/names` (`AppAddress`, `CheckAppAddress`, `ErrAddressTooLong`, `ErrAddressConflict`, con i test).
- **Cambiarlo dopo.** `sudo gitstack config set apps-domain apps.azienda.it` rigenera il certificato (stessa CA, nuovo SAN wildcard) e riscrive `apps_domain:` di `config.yaml` e le scelte salvate; con l'hosting delle app acceso porta il dominio nuovo anche a core. Serve anche dopo l'**aggiornamento dalla v2**, che non ha un dominio: `gitstack upgrade` da solo non lo inventa e `gitstack status` dice «Dominio delle app: non impostato»; con `gitstack upgrade --enable-apps` (o `gitstack config set apps on`) il dominio si indica con `--apps-domain` oppure si prende il predefinito (`apps.<host>`) **con un avviso**. Le app già create mantengono il loro indirizzo.

Prova in CI: `deploy/tests/apps_domain_test.sh` (scelta del dominio e certificato con openssl vero) e `.github/workflows/install.yml` (installazione con e senza `--apps-domain`, upgrade dalla v2 e `config set apps-domain`).

### Hosting delle app: accenderlo (GIT-375, ADR 0002)

Il controller delle app vive in `core` e parte solo con `GITSTACK_CORE_APPS_ENABLED=true`. Il chart lo imposta con `apps.enabled` (**spento di default**) e gli dà i permessi Kubernetes minimi dell'[ADR 0002](../docs/adr/0002-architettura-hosting-app.md); con l'hosting spento `core` non ha nessun permesso sulle app, l'API risponde `503 apps_unavailable` («I deploy delle app non sono abilitati su questa istanza») e nel cluster non si crea niente.

```bash
# installazione nuova
sudo ./deploy/install.sh --host git.azienda.it --apps-domain apps.azienda.it --enable-apps
# installazione esistente (anche v2: il dominio va indicato o si prende il predefinito, con avviso)
sudo gitstack upgrade --enable-apps [--apps-domain apps.azienda.it]
sudo gitstack config set apps on  [--apps-domain apps.azienda.it]
sudo gitstack config set apps off
sudo gitstack status        # «Hosting delle app: acceso|spento»
```

- **`--enable-apps` / `--disable-apps`** (install.sh): una riesecuzione senza opzioni conserva la scelta (`APPS_ENABLED` in `install.conf`, `apps_enabled:` in `config.yaml`). `gitstack upgrade --enable-apps` e `gitstack config set apps on` fanno la stessa cosa su un'installazione esistente e salvano la scelta; per `upgrade`, i dati non si toccano (migrazioni e backup preventivo come sempre). `config set apps off` toglie il controller: le app già create restano nel cluster e nel database, ma `core` non le gestisce più.
- **Valori del chart** (`deploy/gitstack/values.yaml`, sezione `apps`): `apps.enabled`, `apps.domain` (obbligatorio se acceso: lo passano l'installer e i comandi `gitstack`, lo stesso di `--apps-domain`) `apps.authUrl` (indirizzo interno del forward-auth, `GITSTACK_CORE_APPS_AUTH_URL`; vuoto = il Service del gateway della release, `http://<release>-gateway.<ns>.svc.cluster.local:8080/_appauth/verify`) e `apps.signingKeys.existingSecret`. **Login davanti alle app (GIT-347, [docs/apps-login.md](../docs/apps-login.md)):** il controller crea da solo nel namespace di ogni app i Middleware Traefik `gitstack-strip` e `gitstack-auth`; con `apps.enabled` il chart configura anche il gateway: `GITSTACK_GATEWAY_APPAUTH_SIGNING_KEYS` da un Secret `<release>-gateway-appauth-keys` (seme Ed25519 generato una sola volta con `randBytes`, riletto con `lookup` a ogni upgrade, `helm.sh/resource-policy: keep`, quindi stabile negli upgrade e nei ripristini del chart), `GITSTACK_GATEWAY_APPS_DOMAIN` (= `apps.domain`) e `GITSTACK_GATEWAY_PUBLIC_URL` (= `core.env.publicUrl`). Senza le chiavi `/_appauth/verify` risponde 503 e nessuna app `login` passa: per questo non c'è un'opzione per spegnerle. Non serve più nessun Middleware da creare a mano.
- **RBAC minimo** (`templates/apps-rbac.yaml`): nessun permesso sulle app e nessun ClusterRoleBinding verso di esse. Un ClusterRole del controller (`<release>-apps-controller`) dà a `core` solo `create/get/list/watch/update/delete` su `namespaces` (`update` serve ad allineare la label Pod Security di un namespace già creato), `create/get` su `rolebindings` e `bind` sul solo ClusterRole modello. Il ClusterRole modello (`<release>-app-workload`: Deployment, Service, Secret, Pod, `pods/log`, eventi e IngressRoute) non ha binding cluster-wide: dopo aver creato il namespace `gitstack-app-<id>` il controller vi crea un RoleBinding verso di esso, quindi i permessi sulle app esistono solo nei namespace delle app. Con la CI accesa si usa lo stesso ServiceAccount (`ci-dispatcher`); altrimenti il chart crea `<release>-core`. **Perimetro:** RBAC non filtra per nome, quindi `create rolebindings`, `bind` e `update/delete namespaces` varrebbero su tutto il cluster (`update` sui namespace è una deviazione dall'ADR, che non lo elenca). Li chiude una `ValidatingAdmissionPolicy` (`<release>-apps-controller-scope`, Kubernetes >= 1.30, solo con `apps.enabled`) che riguarda il solo ServiceAccount di core: namespace solo `gitstack-app-*` con la label `gitstack.io/managed-app=true` (CREATE/UPDATE sull'oggetto, UPDATE/DELETE sul vecchio oggetto); RoleBinding solo in quei namespace, solo verso il ruolo modello e solo per il ServiceAccount di core. Il test è `deploy/tests/chart_hardening_test.sh` («GIT-375», `helm template`); l'e2e prova i rifiuti con impersonazione (`verify-apps-hosting on`).
- **Priorità e barriere di rete** delle app non sono qui: le classi di priorità sono di GIT-345 e le NetworkPolicy dei namespace delle app di GIT-344.

Prova sulla VM (GIT-355): il passo h di `deploy/test-vm/e2e.ps1` installa con `--enable-apps --apps-domain`, fa il deploy di un’app web di prova, controlla il login GitStack davanti all’app (401 senza credenziale, corpo dell’app con il token), fa il deploy di v2 e il rollback a v1 (righe `h-app ciclo 1/6 … 6/6`); il runbook e il dogfooding del sito della documentazione su homehub sono in `docs/operations.md` («Dogfooding delle app»).

Prova in CI (`install.yml`): installazione con `--enable-apps` e un'app di prova creata da API che risponde 200 al suo indirizzo automatico; upgrade dalla v2 con l'hosting spento (503 e nessun oggetto nel cluster), poi `gitstack upgrade --enable-apps` (24.04) o `config set apps on` (22.04), l'app di prova e i dati del seed ancora lì.

#### Nomi `.local` (mDNS)

Il caso reale di homehub: il router di casa (Sky) non permette record DNS locali, quindi la macchina si chiama `homehub.local`, risolto via mDNS.

- **Sul server** serve `avahi-daemon` attivo e `libnss-mdns`: `sudo apt install avahi-daemon libnss-mdns`. Il nome è quello della macchina (`hostnamectl set-hostname homehub` → `homehub.local`). Verifica: `getent hosts homehub.local` deve dare l'IP della macchina, ed è lo stesso controllo del preflight.
  avahi risponde con gli indirizzi di tutte le interfacce, anche quelle di k3s (`cni0`): il preflight accetta un qualunque indirizzo della macchina, l'IP principale è comunque nei SAN. Per limitarlo: `allow-interfaces=<interfaccia>` in `/etc/avahi/avahi-daemon.conf`.
- **I client** devono supportare mDNS: Windows 10/11, macOS e Linux con `nss-mdns` lo fanno. Altrimenti si usa l'IP (nei SAN) o un nome nel DNS.
- **Il certificato** (CA interna) ha `homehub.local` e l'IP come SAN: `openssl x509 -in /etc/gitstack/tls/server.crt -noout -ext subjectAltName`. Va installata la CA sui client come per ogni nome.
- **I pod di k3s non risolvono `.local`** (CoreDNS non fa mDNS): niente nei servizi deve chiamare dall'interno del cluster il proprio indirizzo pubblico. Le chiamate fra servizi usano i nomi interni (`<release>-core`, ecc.); l'URL pubblico serve solo ai link e ai redirect che vede il client.
- Un nome `.local` non è accettato con `--tls letsencrypt` (non è un nome pubblico).

### k3s e Traefik v3

La versione di k3s è pinnata (mai `latest`) nel file `deploy/k3s-version` (oggi `v1.36.4+k3s1`), unico punto che dichiara con quale k3s è provata ogni versione di GitStack: lo leggono `install.sh` e `gitstack upgrade` (una patch nuova si applica da sola, una minore con `gitstack upgrade --k3s`, vedi `docs/operations.md`). Quella versione include il chart Traefik `40.1.0` (`appVersion: v3.7.0`, cioè Traefik v3), richiesto dal Middleware `traefik.io/v1alpha1` dell'Ingress del chart (`gitstack/templates/ingress.yaml`, vedi `gitstack/README.md`). Sovrascrivibile con la variabile `INSTALL_K3S_VERSION` se serve un'altra versione, purché includa Traefik v3.

k3s installa la CRD `middlewares.traefik.io` (serve al Middleware dell'Ingress) in modo **asincrono**, tramite un HelmChart/Job interni che partono dopo che il nodo è Ready: `install.sh` aspetta esplicitamente che quella CRD compaia e diventi `Established` prima di lanciare `helm upgrade --install` del chart di GitStack, altrimenti su una macchina pulita si incontra la stessa race del job "chart" della CI (`no matches for kind Middleware`). Con la CRD già presente (esecuzioni successive) l'attesa passa subito.

### Helm

Se `helm` non è già presente, lo script scarica il tarball ufficiale pinnato a `GITSTACK_HELM_VERSION` (default `v3.16.3`, stessa versione testata in `gitstack/README.md`) da `get.helm.sh` e ne verifica il checksum `.sha256sum` pubblicato, invece dello script mobile `get-helm-3` di `helm/helm@main`: stesso principio del pin di k3s, niente scaricato da un riferimento non pinnato.

### Immagini: tag sha del commit, non "latest"

Come per k3s, mai un tag mobile. Se `--image-tag`/`GITSTACK_IMAGE_TAG` non è impostato, lo script risolve automaticamente il tag `sha-<sha commit completo>`:

- se lo script gira da un checkout Git locale (il caso tipico di `./deploy/install.sh`), usa lo sha del commit effettivamente presente su disco (`git rev-parse HEAD`);
- altrimenti (nessun checkout locale, es. `curl | sh`) risolve `GITSTACK_REPO@GITSTACK_REF` (default `fathorMB/GitStack@main`) tramite l'API di GitHub.

Per installare una versione di rilascio si usa `--version vX.Y.Z` (vedi sotto): il tag immagine diventa `vX.Y.Z` e la CI lo pubblica accanto a `sha-<commit>`, con lo stesso digest. Il tag `X.Y.Z` senza `v` è quello di default del chart (`appVersion`).

### Installare una versione di rilascio (`--version`, GIT-222)

Un tag Git `vX.Y.Z` (o `vX.Y.Z-rc.N`) produce una GitHub Release firmata con il nome
della versione (binari, chart `gitstack-X.Y.Z.tgz`, bundle cosign, SBOM, `SHA256SUMS`,
changelog: vedi [`../docs/operations.md`](../docs/operations.md), «Rilascio con versione»).

```sh
# Da un checkout o da solo (curl | bash): versione fissata, firme verificate
sudo ./deploy/install.sh --version v1.0.0 --host homehub.local
curl -fsSL https://raw.githubusercontent.com/fathorMB/GitStack/v1.0.0/deploy/install.sh \
  | sudo bash -s -- --version v1.0.0 --host homehub.local

# Aggiornamento di un'installazione esistente
sudo gitstack upgrade --version v1.0.0
```

`--version` (o `GITSTACK_VERSION`) imposta ref del sorgente, tag immagine e release dei
download; la verifica delle firme (V5) resta attiva come per `sha-<commit>` e il chart
firmato deve avere `version` e `appVersion` uguali alla versione. Non si combina con un
`--image-tag` diverso. Per un commit preciso: `--image-tag sha-<commit>`.

### Idempotenza

Rieseguire `install.sh` è sicuro:

- **k3s**: se è già installato (`command -v k3s`), lo script non lo reinstalla — si limita ad avviare il servizio se non è attivo. Aggiornare la versione di k3s di un'installazione esistente non è ancora supportato (M-08).
- **GitStack**: lo script usa sempre `helm upgrade --install`. Il Secret della password di Postgres ha `helm.sh/resource-policy: keep` (vedi `gitstack/README.md`): una seconda esecuzione non la cambia.
- **Porte**: il controllo preliminare sulle porte 80/443/6443 viene saltato quando k3s è già installato (sono occupate dalla stessa installazione, non da un conflitto).

### A fine installazione

Lo script stampa l'URL della UI (`https://<ip-macchina>/`, o il nome di `--host`), l'URL di salute dell'API (`/api/healthz`), le istruzioni per fidarsi della CA interna con la sua impronta SHA-256 e i comandi per verificare lo stato: `kubectl get pods`, `helm status`, `curl` sull'health endpoint, `journalctl -u k3s`.

### Comando `gitstack` e configurazione

L'installer scrive `/etc/gitstack/config.yaml` (root-only, `0600`) e installa in `/usr/local/bin/gitstack` il comando di amministrazione (`admin/`, GIT-142) con il checksum SHA-256 verificato (`--admin-binary`, `--admin-sha256`; se non c'è un binario pubblicato lo salta con un avviso). Dopo l'installazione: `sudo gitstack status`. Formato del file, build e codici di uscita: [`../admin/README.md`](../admin/README.md).

### Sicurezza: HTTPS, CA interna, utente admin

**HTTPS (N5, GIT-143).** Senza opzioni l'installer crea una CA interna (chiave in `/etc/gitstack/tls/ca.key`, solo root, mai in Kubernetes) e un certificato per il nome dell'host e il suo IP; Traefik serve la 443 e reindirizza la 80. Il certificato della CA si scarica da `/downloads/ca.crt`. Opzioni: `--host NOME|IP` (SAN e URL pubblico, es. `homehub.local`), `--tls letsencrypt` (+ `--tls-email`), `--tls-cert`/`--tls-key` (certificato del cliente), `--insecure-http` (solo prove locali, con avviso nella UI). Il rinnovo del certificato interno è automatico (`gitstack-tls-renew.timer`, ogni notte, a meno di 30 giorni dalla scadenza). Procedura per fidarsi della CA su Linux, macOS e Windows, rinnovo, Let's Encrypt: [`../docs/tls.md`](../docs/tls.md). Lo script `gitstack-tls.sh` (installato in `/usr/local/sbin/gitstack-tls`) fa CA, emissione e rinnovo.

**Utente admin (GIT-35).** Al primo avvio, su un database senza nessun amministratore, `identity` crea l'utente `admin`. La password iniziale la genera il chart una sola volta nel Secret `<release>-identity-admin` (`helm.sh/resource-policy: keep`): l'installer non la stampa mai, mostra solo il comando per leggerla:

```sh
k3s kubectl -n default get secret gitstack-identity-admin -o jsonpath='{.data.password}' | base64 -d; echo
```

Al primo login la password va cambiata: finché non lo fai ogni altra chiamata risponde 403 `password_change_required`. Rieseguire `install.sh` non cambia né il Secret né l'admin.

### Postgres esterno (N8, GIT-205)

`install.sh --postgres-url postgres://UTENTE@HOST:5432/DATABASE[?sslmode=...]` usa un PostgreSQL 15, 16 o 17 del cliente al posto del bundle. Opzioni: `--postgres-password-file FILE` (o `GITSTACK_POSTGRES_PASSWORD`: la password non passa dalla riga di comando), `--postgres-ca-file FILE` (CA privata per `verify-ca`/`verify-full`); `sslmode` di default `require`.

- **Preparazione (DBA).** [`postgres-prepare.sql`](postgres-prepare.sql) crea il database dedicato e un utente `LOGIN CREATEROLE` non superuser; è idempotente e la password arriva da `GITSTACK_DB_PASSWORD` (`\getenv`, serve `psql` 15+).
- **Preflight.** Dopo aver scaricato e verificato il comando `gitstack` e prima di installare k3s, l'installer esegue `gitstack postgres check`: versione 15–17, connessione e le prove reali (in una transazione annullata) di creare uno schema e un ruolo. Rifiuta la versione 14 e i permessi insufficienti con un messaggio che dice cosa fare; `--skip-preflight` lo salta con un avviso.
- **Cosa crea GitStack.** Come con il bundle: lo schema `core` (dell'utente configurato) e il ruolo `identity_app` con il suo schema `identity` (initContainer `bootstrap-db` di identity, ora valido anche per un utente non superuser su PostgreSQL 15, 16 e 17). Password e CA vanno in Secret fuori da Helm (`<release>-postgres-external`, `<release>-postgres-ca`).
- **Pod client.** Con il database esterno il chart aggiunge `<release>-pgclient` (`psql` e `pg_dump`, stessa versione principale del server, impostata dall'installer): lo usano `gitstack backup`, `restore`, `upgrade` (backup preventivo e rollback degli schemi) e `status`.
- **Riesecuzione.** Senza gli stessi `--postgres-*` l'installer si ferma: un `helm upgrade` senza i valori riporterebbe a un bundle vuoto.

Dettagli operativi: [`../docs/operations.md`](../docs/operations.md#external-postgresql).

### Opzioni principali

**Rete aziendale (N4, N9a, GIT-206).** `--http-proxy`, `--https-proxy`, `--no-proxy` (download di k3s e Helm, containerd di k3s e pod di GitStack), `--registry-mirror` con `--registry-username`/`--registry-password-file` (tutte le immagini, di sistema di k3s comprese, via `registries.yaml`), `--download-mirror` (binari di k3s, Helm e gitstack) e `--ca-bundle` (CA aziendale: sistema, containerd, pod). Il preflight prova a raggiungere proxy e mirror e dice cosa non risponde; dopo l'installazione si cambia con `gitstack config set network`. Struttura del mirror, esempi e prova senza uscita diretta (`test-network/run.sh`): [`../docs/operations.md`](../docs/operations.md), sezione «Corporate network». I valori Helm sono `proxy.*` e `corporateCA.configMap` (GIT-207).

`install.sh --help` le elenca tutte. Le più usate: `--force`, `--skip-preflight`, `--release-name`, `--namespace`, `--image-tag`, `--version` (versione di rilascio), `--image-registry` (mirror, M-08), `--host`, `--apps-domain` (dominio delle app, vedi sopra), `--tls`, `--tls-cert`/`--tls-key`, `--insecure-http` (HTTPS, vedi sopra), `--postgres-url`/`--postgres-password-file`/`--postgres-ca-file` (Postgres esterno, vedi sopra), `--values`/`--set` (valori Helm aggiuntivi).

### CI integrata (v2-a, GIT-285)

La CI integrata (workflow in `.gitstack/workflows/`, job in pod usa e getta nel namespace `gitstack-ci-jobs`) è **spenta di default**: l'installer la accende con `--ci`, `gitstack upgrade` con `--set ci.enabled=true`.

| Opzione | Significato |
|---|---|
| `--ci`, `--no-ci` | accende, spegne. Senza nessuna delle due una riesecuzione **conserva** la scelta (il namespace dei job esiste) e i posti già configurati; la prima installazione è senza CI |
| `--ci-capacity N` | job in parallelo, da 1 a 3 |
| `--ci-build` | accende anche le build rootless (BuildKit) nel namespace `gitstack-ci-build` |
| `--ci-action-mirror URL` | mirror interno delle action (`uses:`) invece di github.com (N4, N9) |
| `--ci-storage-size SIZE` | volume dei log (default `20Gi`) |
| `--ci-s3-endpoint`, `--ci-s3-bucket`, `--ci-s3-region`, `--ci-s3-prefix` | S3 per artefatti e cache (N7). Le chiavi stanno in `GITSTACK_CI_S3_ACCESS_KEY` e `GITSTACK_CI_S3_SECRET_KEY`, mai da argomento; l'installer crea il Secret `<release>-ci-s3` |

**Posti dei job nei due profili (N1, CI7).** Senza `--ci-capacity`: **3 posti** con il profilo consigliato (almeno 8 vCPU **e** 16 GB), **1 posto** con il minimo (4 vCPU, 8 GB) o se solo uno dei due numeri arriva al consigliato. Mai oltre 3.

**Spazio dei job e priorità.** Ogni posto è un pod da 2 vCPU, 4 GiB e 20 GiB di disco effimero (ResourceQuota `ci-jobs`, LimitRange `ci-job-limits`). I pod usano la PriorityClass `gitstack-ci-job` (1000, senza preemption), sotto `gitstack-apps` e `gitstack-services`: con poca memoria sono i job a essere sfrattati. Il preflight avvisa (non si ferma) se i GB liberi sono meno di 20 più 20 per ogni posto.

**Rete aziendale.** Proxy e CA dati all'installer arrivano anche ai pod dei job: core passa `HTTP_PROXY`, `HTTPS_PROXY` e `NO_PROXY`, il chart copia il ConfigMap della CA nel namespace dei job (`corporate-ca`, montato in `/etc/gitstack/corporate-ca`, `SSL_CERT_DIR` e `NODE_EXTRA_CA_CERTS`). L'esecutore (`act` e `ci-runner`) sta **nell'immagine** `gitstack-ci-runner`, pubblicata e firmata da `ci.yml` con lo stesso tag dei servizi e verificata da `install.sh` e `gitstack upgrade` come le altre: con `--registry-mirror` arriva dal registry interno. Le action `uses:` si scaricano dal mirror di `--ci-action-mirror`; con un IPv4 l'installer apre anche l'uscita dei job verso `<ip>/32`, con un nome serve `--values` (`ci.runner.actionMirrorEgress`). Lo stesso vale per il proxy: con `--http-proxy`/`--https-proxy` su un IPv4 l'installer apre l'uscita dei job verso `<ip>/32` sulla porta del proxy (`ci.runner.proxyEgress`); con un proxy indicato **con un nome** su una rete privata la regola non si può dedurre (l'installer non risolve il nome) e va scritta a mano con `--values`, per esempio `ci.runner.proxyEgress: [{to: [{ipBlock: {cidr: 10.0.0.5/32}}], ports: [{protocol: TCP, port: 3128}]}]`. Senza la regola i job non raggiungono il proxy, perché la policy di base chiude 10/8, 172.16/12 e 192.168/16.

**Aggiornare dalla v1.x con la CI.** `sudo gitstack upgrade --to <versione> --set ci.enabled=true --set ci.capacity=1`: backup preventivo, k3s, Helm e rollback come sempre; i valori `--set` restano per gli upgrade successivi. Se l'upgrade fallisce il rollback (uscita 7) torna alla revisione di prima, senza CI. **Backup e restore** includono i log dei job (`ci-logs.tar`); artefatti e cache no. Dettagli: [`../docs/operations.md`](../docs/operations.md), «Built-in CI: install, upgrade, backup». Prova: `tests/ci_install_test.sh` (scelte dell'installer, senza root) e `install.yml` (installazione, workflow di prova, rollback, upgrade, backup e restore con la CI, su 22.04 e 24.04).

### Firme (V5, GIT-209)

Oltre allo SHA-256, `install.sh` verifica le firme cosign di **binario `gitstack`**
(bundle `<sorgente>.sigstore.json` accanto al binario), **chart** (il `.tgz` della
release `sha-<commit>`, o `vX.Y.Z` con `--version`, col suo bundle, verificato prima di estrarlo) e **immagini**
`gitstack-*` (prima di `helm upgrade --install`), con l'identità del workflow `ci.yml` del
repository e l'issuer OIDC di GitHub Actions fissati. Usa un cosign `v2.4.0` pinnato
(SHA-256 verificato come per Helm), installato in `/usr/local/bin/gitstack-cosign` e
ritrovato da `gitstack upgrade` tramite `v5.cosign_path` nel config. Una firma mancante,
non valida o di un'altra identità ferma l'installazione senza toccare il sistema.

Per un mirror interno che non ha le firme: `--allow-unsigned-mirrors` (o
`GITSTACK_ALLOW_UNSIGNED=1`), eccezione esplicita e mai il default: salta le verifiche
cosign con un avviso, si salva in `v5.allow_unsigned` e `gitstack status` la segnala.
Rilanciare l'installer senza il flag la toglie. Con `--chart-dir` o un checkout locale il
chart non è un artefatto firmato e non si verifica (avviso); le immagini sì. Dettagli in
[../docs/operations.md](../docs/operations.md), sezione V5.

### Verifica in CI

`.github/workflows/ci.yml`, job `shellcheck`: `shellcheck deploy/install.sh deploy/gitstack-tls.sh deploy/tests/preflight_test.sh` (e gli altri script di shell del repository), poi `deploy/tests/preflight_test.sh`: prova i profili di N1, il sistema non supportato e la verifica del nome senza root né VM (le letture di CPU, RAM, disco, DNS sono funzioni che il test ridefinisce). Non verifica un'installazione reale (serve un sistema con systemd, non disponibile nei runner container-based): quella si fa sulla VM di `test-vm/` (GIT-12) o su una VM/container Ubuntu 24.04 usa e getta, vedi il riepilogo dell'item GIT-9.

## Windows via WSL2 (W1, W2 — prova/demo)

Script in `deploy/windows/` per installare GitStack su Windows 10/11 tramite WSL2. **Solo per prove, demo e piccoli team**: WSL2 in produzione ha limiti noti di rete (IP dinamico, firewall non esposto, servizi non affidabili) ed è documentato come rischio in D14 [c_cf5bc915298b97db].

### Requisiti

- Windows 10 build 19041 (2004) o superiore per WSL2; build 22621 (Windows 11 22H2) per `-ShareOnNetwork` (rete mirrored)
- Virtualizzazione attiva nel BIOS/UEFI
- PowerShell 5.1+ (incluso in Windows) o pwsh

### Uso

```powershell
# Installazione di prova (solo accesso locale, https://localhost, SSH 2222):
.\deploy\windows\install-wsl.ps1

# Con rete mirrored (porte 443 e 2222 esposte nel firewall, Windows 11 22H2+):
.\deploy\windows\install-wsl.ps1 -ShareOnNetwork

# Disinstallazione:
.\deploy\windows\install-wsl.ps1 -Uninstall
```

Lo script:
1. Verifica e abilita WSL2 se necessario;
2. Installa Ubuntu 24.04 (o 22.04 come fallback);
3. Abilita systemd in `/etc/wsl.conf` dentro la distro;
4. Lancia `deploy/install.sh` dentro WSL con `--skip-preflight` (l'hardware di un PC Windows non ha Ubuntu):
5. Opzionalmente configura `networkingMode=mirrored` con regole firewall per 443/2222;
6. Registra un'attività di Windows per avviare WSL e GitStack all'accesso utente.

### Prova del board (Windows 11 con WSL2)

I runner CI non hanno WSL2 reale; per la prova vera su un PC Windows 11 22H2+:

```powershell
# 1. Da terminale PowerShell come amministratore:
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass
cd <percorso>\GitStack

# 2. Installazione:
.\deploy\windows\install-wsl.ps1

# 3. Verifica accesso locale:
curl.exe -k https://localhost
# Verifica che k3s sia attivo:
wsl -d GitStack -u root -- systemctl is-active k3s

# 4. Con rete mirrored (richiede Windows 11 22H2, build ≥ 22621):
.\deploy\windows\install-wsl.ps1 -ShareOnNetwork
# Da un altro PC nella stessa rete:
curl.exe -k https://<IP-del-PC>

# 5. Disinstallazione:
.\deploy\windows\install-wsl.ps1 -Uninstall
# Verificare che:
# - la distro "GitStack" sia stata rimossa (wsl -l -q)
# - le regole firewall GitStack-WSL-443 e GitStack-WSL-2222 siano rimosse
# - l'attivita pianificata "GitStack-WSL-Login" sia rimossa
# - le righe marcate # GitStack siano rimosse da .wslconfig (il file resta)
```
