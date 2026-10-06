# HTTPS di GitStack (N5, GIT-143)

Di default `deploy/install.sh` installa GitStack in HTTPS. Questa pagina spiega le tre modalità, il rinnovo del certificato e come fidarsi della CA interna sui client. Pagina di riferimento per l'operatore; la pagina di distribuzione della UI (G6) la riprenderà.

## Cosa fa l'installer

| Modalità | Opzione | Certificato |
|---|---|---|
| CA interna (default) | nessuna | CA generata dall'installer; certificato per il nome dell'host e l'IP |
| Let's Encrypt | `--tls letsencrypt --host git.example.com [--tls-email me@example.com]` | pubblico, ottenuto e rinnovato da Traefik (HTTP-01) |
| Certificato del cliente | `--tls-cert FILE --tls-key FILE` | quello fornito (PEM) |
| Solo HTTP | `--insecure-http` | nessuno: solo prove locali, la UI mostra un avviso |

In tutte le modalità con TLS Traefik serve la 443 e reindirizza la 80 alla 443 (308 permanente). Fa eccezione `/downloads/ca.crt` (solo con la CA interna), servito anche in HTTP perché serve proprio a fidarsi di HTTPS la prima volta. Gli URL pubblici di core (indirizzi di clone, link nelle email) diventano `https://<host>`: `core.env.publicUrl` e `identity.oidc.publicUrl` li imposta l'installer.

`--host NOME|IP` (ripetibile) sceglie i nomi che finiscono nei SAN del certificato; il primo è quello dell'URL pubblico. Senza `--host` i SAN sono: nome breve dell'host, `<nome>.local`, nome completo se diverso, e l'IP principale; l'URL pubblico è l'IP. L'IP principale è sempre fra i SAN, anche con `--host`. Esempio per un server raggiunto via mDNS (avahi):

```sh
sudo ./deploy/install.sh --host homehub.local
```

I servizi dentro il cluster non usano l'URL pubblico (parlano tra loro con i nomi dei Service): i pod non devono risolvere `.local`.

Una riesecuzione dell'installer senza opzioni TLS conserva modalità, `--host` ed email scelti la volta prima (`/etc/gitstack/tls/install.conf`).

## CA interna: dove sta cosa

Tutto sta in `/etc/gitstack/tls/` (cartella `0700`, root):

| File | Contenuto |
|---|---|
| `ca.key` | chiave della CA, `0600`. **Solo sull'host**: non va mai in un Secret né in un backup non protetto |
| `ca.crt` | certificato pubblico della CA (EC P-256, 10 anni) |
| `server.crt`, `server.key` | certificato e chiave del server (397 giorni) |
| `server.sans` | elenco SAN del certificato corrente |
| `tls.conf` | parametri di `gitstack-tls` (non si include con `source`: legge solo chiavi note) |
| `install.conf` | scelte dell'installer, per le riesecuzioni |

In Kubernetes (namespace della release, `default` di default; `<release>` = `gitstack` di default):

- Secret `<release>-tls` (`kubernetes.io/tls`): certificato e chiave del server, letti da Traefik.
- ConfigMap `<release>-ca` (chiave `ca.crt`): certificato pubblico della CA, montato dal pod `web` e servito su `/downloads/ca.crt`.

La chiave della CA **non** è in Kubernetes: chi fa backup e ripristino (`gitstack backup`/`restore`) deve includere `/etc/gitstack/tls/ca.key` e `ca.crt`, altrimenti dopo un ripristino su un'altra macchina i client dovrebbero fidarsi di una CA nuova.

## Rinnovo (automatico)

L'installer attiva `gitstack-tls-renew.timer` (systemd): ogni notte alle 03:30, con un ritardo casuale fino a 30 minuti, esegue `gitstack-tls renew`. Se il certificato del server scade entro 30 giorni, o è cambiato l'elenco dei SAN, o non è più firmato dalla CA, ne emette uno nuovo e aggiorna il Secret: Traefik lo rilegge da solo, senza riavvii. Se il timer manca (host senza systemd) l'installer avvisa: pianifica a mano `/usr/local/sbin/gitstack-tls renew` almeno una volta al giorno.

```sh
sudo gitstack-tls status          # modalità, SAN, scadenze e impronte SHA-256
sudo gitstack-tls ensure          # forza ora il controllo e la pubblicazione
sudo gitstack-tls fingerprint     # impronta SHA-256 della CA
systemctl list-timers gitstack-tls-renew.timer
journalctl -u gitstack-tls-renew.service
```

I client non devono rifare niente quando cambia il certificato del server: si fidano della CA, che dura 10 anni.

### Rotazione della CA

`gitstack-tls` avvisa quando la CA scade entro 90 giorni. Per ruotarla: ferma il timer, sposta via `ca.key`/`ca.crt`/`server.*` da `/etc/gitstack/tls/`, rilancia `sudo gitstack-tls ensure` (genera una CA nuova e un certificato nuovo), poi ridistribuisci `/downloads/ca.crt` ai client: ognuno deve fidarsi della CA nuova.

## Certificato del cliente

`--tls-cert`/`--tls-key` (PEM, chiave senza passphrase) vengono copiati in `/etc/gitstack/tls/custom.crt` e `custom.key`. L'installer li convalida: la chiave deve corrispondere al certificato, non deve essere scaduto, e avvisa se i SAN non coprono i nomi o l'IP indicati. Il rinnovo spetta al cliente: sostituisci i due file e lancia `sudo gitstack-tls ensure` (il timer notturno lo fa comunque, e avvisa quando mancano meno di 30 giorni).

## Let's Encrypt

`--tls letsencrypt --host git.example.com` accetta solo un nome pubblico (non un IP, non `.local`/`.lan`/`.internal`). L'installer scrive `/var/lib/rancher/k3s/server/manifests/gitstack-traefik-letsencrypt.yaml`, un `HelmChartConfig` di k3s per il Traefik incluso: resolver `letsencrypt` (HTTP-01 sull'entrypoint `web`), `acme.json` su un volume persistente da 128 Mi. Servono le porte 80 e 443 raggiungibili da internet e un record DNS che punti all'host. Il chart mette sull'Ingress e sulla rotta git `certResolver: letsencrypt`; Traefik rinnova da solo. L'indirizzo ACME di test si imposta con `GITSTACK_ACME_CA_SERVER` (staging di Let's Encrypt).

Questa modalità è documentata e implementata ma non è stata provata con un'emissione reale (richiede un dominio pubblico). Diagnostica: `k3s kubectl -n kube-system logs deploy/traefik | grep -i acme`.

## Solo HTTP (`--insecure-http`)

Solo per prove locali: nessun TLS, nessun redirect, URL pubblici `http://`. La UI mostra in cima un avviso («Connessione non protetta») quando la pagina è servita in HTTP su un host non locale (`localhost`, `127.x`, `::1` non lo fanno scattare). Il cookie di sessione è `Secure` solo se il gateway sa che la richiesta è arrivata in HTTPS (vedi sotto).

## Header `X-Forwarded-Proto`

In HTTPS Traefik termina il TLS e inoltra al gateway e a identity con `X-Forwarded-Proto: https` (lo imposta da solo in base alla connessione reale, e non si fida di quello inviato dal client perché l'entrypoint non ha `forwardedHeaders.trustedIPs`). Gateway e identity si fidano di questo header solo da peer nella rete dei pod (`GITSTACK_GATEWAY_TRUSTED_PROXIES`, `GITSTACK_IDENTITY_TRUSTED_PROXIES`, default `10.42.0.0/16`): è la condizione perché il cookie di sessione esca `Secure`.

## Come fidarsi della CA sui client

1. Scarica il certificato: `http://<host>/downloads/ca.crt` (o `https://` con `-k` solo per questo scaricamento, o copialo da `/etc/gitstack/tls/ca.crt`).
2. **Verifica l'impronta SHA-256** con quella stampata dall'installer (o `sudo gitstack-tls fingerprint` sul server): senza questo controllo chi intercetta la prima richiesta può darti una CA falsa.

   ```sh
   openssl x509 -in ca.crt -noout -fingerprint -sha256
   ```

### Linux (Debian/Ubuntu)

```sh
sudo cp ca.crt /usr/local/share/ca-certificates/gitstack-ca.crt
sudo update-ca-certificates
```

Fedora/RHEL: copia in `/etc/pki/ca-trust/source/anchors/` e lancia `sudo update-ca-trust`. Questo copre `curl`, `git` (backend OpenSSL) e i browser basati su NSS tramite il sistema; Firefox ha un proprio archivio: Impostazioni → Privacy e sicurezza → Certificati → Importa (oppure `about:config`, `security.enterprise_roots.enabled = true`). Chrome/Chromium su Linux usa l'archivio NSS dell'utente: `certutil -d sql:$HOME/.pki/nssdb -A -t "C,," -n gitstack -i ca.crt`.

### macOS

```sh
sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain ca.crt
```

Copre Safari, Chrome e `git` (Apple SSL). Firefox: abilita `security.enterprise_roots.enabled` o importa la CA a mano.

### Windows (PowerShell come amministratore)

```powershell
Import-Certificate -FilePath .\ca.crt -CertStoreLocation Cert:\LocalMachine\Root
```

Copre Edge, Chrome e `git` con il backend `schannel`. Git for Windows con il backend OpenSSL (default dell'installer di Git for Windows) usa un proprio file: aggiungi la CA a `C:\Program Files\Git\mingw64\etc\ssl\certs\ca-bundle.crt`, oppure `git config --global http.sslCAInfo C:\percorso\ca.crt`, oppure passa a schannel: `git config --global http.sslBackend schannel`.

### Solo per git

Senza toccare il sistema: `git config --global http.sslCAInfo /percorso/ca.crt`, oppure per un solo host `git config --global http.https://<host>/.sslCAInfo /percorso/ca.crt`. Per un singolo comando: `git -c http.sslCAInfo=/percorso/ca.crt clone https://<host>/<owner>/<repo>.git`.

### curl

`curl --cacert ca.crt https://<host>/api/healthz`.

## Provato su

- `deploy/gitstack-tls.sh`: emissione, riemissione al cambio dei SAN e a ridosso della scadenza, certificato del cliente (chiave non corrispondente rifiutata, SAN non coperti segnalati), permessi dei file, in un container Ubuntu 24.04 con `kubectl` finto.
- `install.sh`: scelta di modalità, `--host`, combinazioni non valide (`resolve_tls`), in un container Ubuntu 24.04.
- Chart: `helm template` per HTTP (default invariato), CA interna, Let's Encrypt e il rifiuto di `tls.enabled` senza Secret né resolver.
- La prova completa su VM (redirect 80→443, UI/API/git clone in HTTPS, `--tls-cert`, `--insecure-http`) è `deploy/test-vm/e2e.ps1`, lanciato dal board; la prova con un nome `.local` è in GIT-144.
