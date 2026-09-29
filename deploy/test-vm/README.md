# deploy/test-vm/

Script versionati che creano, ripristinano e distruggono la VM Linux di test
usata per provare l'installer di GitStack (GIT-9) e il test end-to-end
(GIT-11) su una macchina pulita e riproducibile. Il board non crea la VM a
mano: lancia questi script da PowerShell come amministratore, sulla propria
macchina Windows 11 Pro con Hyper-V.

Milestone [c_8289c650b4118599] (M-01), item GIT-12.

## Contenuto

| File | Cosa fa |
|---|---|
| `new-vm.ps1` | Crea la VM (Generazione 2, Ubuntu Server 24.04 LTS via cloud-init) e cattura lo checkpoint `clean`. Idempotente: se la VM esiste già, si ferma con un messaggio chiaro. |
| `reset-vm.ps1` | Riporta la VM al checkpoint `clean`, la riavvia e stampa il suo IP (KVP, o MAC → ARP in fallback). Da lanciare prima di ogni prova di GIT-9/GIT-11. |
| `remove-vm.ps1` | Rimuove la VM, i suoi checkpoint e i file (VHDX, ISO seed, cartella di configurazione). Chiede conferma a meno di `-Force`; se un file resta bloccato ritenta fino a 30s e avvisa se non basta (vedi sotto). |
| `cloud-init/user-data.yaml.tmpl` | Template cloud-init: utente, chiave SSH, sudo senza password, nessun pacchetto extra. `{{...}}` sostituiti da `new-vm.ps1`. |
| `cloud-init/meta-data.yaml.tmpl` | Template cloud-init: hostname e instance-id. |
| `lib/common.ps1` | Funzioni condivise (attesa IP/SSH/cloud-init, creazione ISO seed, controlli). Non va eseguito direttamente. |

Nessun segreto in questa cartella: la chiave pubblica SSH si passa come
parametro allo script (`-SshPublicKey` o `-SshPublicKeyPath`); niente chiavi
private o password sono mai lette, scritte o committate. Il `.gitignore`
locale esclude comunque `*.vhd(x)`/`*.iso`/`image-cache/` per difesa in
profondità, anche se i file generati vivono di default fuori dal repository
(cache immagine in `%LOCALAPPDATA%`, VM in `C:\HyperV-VMs\...`).

## Prerequisiti

- Windows 11 Pro con **Hyper-V attivo** (funzionalità di Windows "Hyper-V").
- PowerShell aperto **come amministratore** (gli script iniziano con
  `#Requires -RunAsAdministrator` e si fermano subito altrimenti).
- Client **OpenSSH** disponibile (`ssh` nel PATH): su Windows 11 è una
  funzionalità opzionale ("Client OpenSSH").
- Una coppia di chiavi SSH del board: la **chiave pubblica** si passa come
  parametro a `new-vm.ps1`; la **chiave privata** resta sulla macchina del
  board, caricata nel proprio `ssh-agent` o nei percorsi predefiniti
  (`~/.ssh/id_ed25519`, `~/.ssh/id_rsa`) — `new-vm.ps1` e `reset-vm.ps1`
  richiamano `ssh` con l'utente locale del board per verificare che la VM sia
  pronta, quindi la usano.
- Connessione a Internet per scaricare la cloud image Ubuntu 24.04 LTS
  (~600 MB, messa in cache locale dopo il primo download, con lo SHA256
  riverificato contro `SHA256SUMS` a ogni esecuzione).
- Uno switch virtuale Hyper-V esistente (di default `Default Switch`, sempre
  presente quando Hyper-V è attivo).
- **Docker Desktop (motore Linux) o WSL2**, per convertire l'immagine cloud
  `.img` (qcow2) in VHDX dinamico: vedi [Conversione immagine e spazio
  richiesto](#conversione-immagine-qcow2--vhdx-dinamico-e-spazio-richiesto).
  Nessuno strumento va installato a mano sull'host: la conversione gira in
  un container.

## Conversione immagine (qcow2 → VHDX dinamico) e spazio richiesto

Dal 28/09/2026 Canonical non pubblica più, per Ubuntu 24.04 LTS generico
(non Azure), un archivio `.vhd.tar.gz` pronto per Hyper-V: l'URL storico
`releases/24.04/release/...vhd.tar.gz` risponde 404 (redirect 302 verso
`releases/noble/release/...`, poi 404). In `releases/noble/release/`
l'unico `.vhd.tar.gz` rimasto è `ubuntu-24.04-server-cloudimg-amd64-**azure**.vhd.tar.gz`:
ha gli strumenti KVP ma porta `walinuxagent` e cloud-init vincolato al
datasource Azure, quindi **ignora il seed NoCloud** generato da questo
script (niente utente, niente chiave) — non va usato.

`new-vm.ps1` usa quindi l'immagine generica
**`ubuntu-24.04-server-cloudimg-amd64.img`** (qcow2, accetta NoCloud) da
`https://cloud-images.ubuntu.com/releases/noble/release/` (`noble` = nome in
codice di 24.04; è un puntatore mobile che Canonical aggiorna in-place a ogni
point release, non una cartella datata) e la converte in VHDX dinamico con
un container Docker:

```powershell
docker run --rm `
  -v "<ImageCacheDir>:/src-in:ro" -v "<VmPath>:/dst-out" `
  alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc `
  sh -c "apk add --no-cache qemu-img && qemu-img convert -f qcow2 -O vhdx -o subformat=dynamic /src-in/<file>.img /dst-out/<VmName>.vhdx"
```

Motivazione della scelta (immagine pinnata **per digest**, non per tag
mobile `alpine:3.20`): non esiste su Docker Hub un'immagine "qemu-img" di
terzi abbastanza affidabile da pinnare (cercate: nessuna con adozione o
manutenzione significativa). `alpine` è l'immagine ufficiale Docker più
piccola con `qemu-img` disponibile nei suoi repository `apk` ufficiali;
pinnare il digest della base fissa esattamente cosa gira, mentre il
pacchetto `qemu-img` (~9.0.2 su Alpine 3.20, pochi MB) si installa al volo
nel container, senza persistere nulla sull'host. Fallback dichiarato ma non
implementato: convertire con `qemu-img` dentro WSL2 (motivare nel riepilogo
se usato al posto di Docker).

**Limite noto sui bind mount**: Docker Desktop (backend WSL2, verificato in
questa sessione) accetta bind mount con percorso Windows nativo diretto,
es. `-v F:\HyperV-VMs\image-cache:/src-in:ro` — **non** il percorso POSIX
delle shell bash su Windows (`/f/HyperV-VMs/...`), che non viene risolto
allo stesso modo. Se usi il backend Hyper-V invece di WSL2, verifica in
Docker Desktop → *Settings → Resources → File sharing* che il volume
(es. `F:`) sia condiviso, altrimenti il mount fallisce silenziosamente o il
container vede una cartella vuota.

`new-vm.ps1` controlla lo spazio libero **prima** di scaricare/convertire,
sui volumi di `-ImageCacheDir` (dimensione dell'immagine +20% di margine,
letta con una richiesta `HEAD`) e di `-VmPath` (`-DiskGB` + 5 GB di margine,
perché il checkpoint `clean` e le prove dell'installer possono far crescere
il VHDX dinamico fino alla dimensione piena). Se lo spazio non basta lo
script si ferma con un `FAIL` che riporta GB liberi e richiesti, es.:

```
Spazio insufficiente su 'C:' per il VHDX e il checkpoint (-VmPath, fino a -DiskGB) (C:\HyperV-VMs\gitstack-test-vm): 19.9 GB liberi, servono almeno 65 GB.
```

Esempio per il board, con VM e cache immagine sul volume con più spazio
libero (`F:` invece di `C:`, che nella diagnosi di GIT-25 aveva solo ~19 GB
liberi contro i ~30+ GB dell'immagine estratta):

```powershell
.\new-vm.ps1 -SshPublicKeyPath "$env:USERPROFILE\.ssh\id_ed25519.pub" `
  -VmPath F:\HyperV-VMs\gitstack-test-vm -ImageCacheDir F:\HyperV-VMs\image-cache
```

Se la chiave privata ha un nome diverso da quelli predefiniti (es. `gitstack_vm`),
passa il suo **percorso** con `-SshPrivateKeyPath`: viene usato solo come
`ssh -i <percorso> -o IdentitiesOnly=yes` per attendere cloud-init (lo script non
legge mai il contenuto del file; senza il parametro si usano ssh-agent e chiavi
predefinite):

```powershell
.\new-vm.ps1 -SshPublicKeyPath "$env:USERPROFILE\.ssh\gitstack_vm.pub" `
  -SshPrivateKeyPath "$env:USERPROFILE\.ssh\gitstack_vm"
```

## IP della VM senza KVP (MAC → cache ARP dell'host)

L'immagine cloud generica (sopra) non ha `linux-cloud-tools`/`hv_kvp_daemon`
installato: `Get-VMNetworkAdapter -VMName <nome> | Select IPAddresses`
resta vuoto per tutta la vita della VM, quindi il vecchio meccanismo (solo
KVP) andava sempre in timeout. `Wait-VmIPv4Address` (`lib/common.ps1`, usata
da `new-vm.ps1` e `reset-vm.ps1`) ora:

1. Prova KVP per un tempo breve (`-KvpTimeoutSeconds`, default 30s) — resta
   il primo tentativo, usato se un domani l'immagine cambia o KVP viene
   installato via cloud-init.
2. Se KVP non risponde, passa al fallback: legge il/i MAC address
   dell'adattatore di rete Hyper-V della VM (`Get-VMNetworkAdapter`,
   normalizzato in formato `AA-BB-CC-DD-EE-FF`) e lo cerca nella cache
   ARP/vicinato dell'host sull'interfaccia `vEthernet (<SwitchName>)`
   (`Get-NetNeighbor`, stati `Reachable`/`Stale`/`Delay`/`Probe`/`Permanent`).
3. Se la cache è vuota (o non contiene ancora quel MAC), la popola con uno
   sweep "leggero": ping asincroni (`System.Net.NetworkInformation.Ping`,
   nessun processo esterno) verso ogni indirizzo host della subnet
   configurata sull'host per quell'interfaccia — calcolata da
   `Get-NetIPAddress` a ogni esecuzione, **mai codificata**, perché la
   subnet del `Default Switch` cambia a ogni riavvio dell'host (verificato
   in questa sessione: `172.20.144.0/20`, 4094 host, sweep completo in
   ~2.7s con ping async a 300ms di timeout — vedi "Limiti noti" sotto).
4. Prima di restituire l'IP trovato via ARP, conferma con una connessione
   TCP `:22`: dopo il ripristino di un checkpoint può restare in cache una
   voce ARP di un avvio precedente (stesso IP DHCP, MAC diverso o VM
   diversa sullo stesso switch); se la porta non risponde ancora, lo script
   continua ad attendere invece di restituire un IP sbagliato.

In timeout, il messaggio riporta il/i MAC cercati, l'interfaccia host e la
subnet calcolata. `-SwitchName` va passato uguale a `new-vm.ps1` sia a
`reset-vm.ps1` (nuovo parametro, default `Default Switch`) sia — se lo
cambi dal default — a `e2e.ps1` di GIT-11 quando usa `-SkipReset` (chiama
`Wait-VmIPv4Address` direttamente senza passare `-SwitchName`: con lo
switch di default funziona senza modifiche a GIT-11).

## Uso

```powershell
# Prima esecuzione: crea la VM e lo checkpoint 'clean'
.\new-vm.ps1 -SshPublicKeyPath "$env:USERPROFILE\.ssh\id_ed25519.pub"

# Prima di ogni prova di GIT-9 / GIT-11: riparti da uno stato pulito
.\reset-vm.ps1

# Quando non serve più
.\remove-vm.ps1
```

### Parametri principali di `new-vm.ps1`

| Parametro | Default | Note |
|---|---|---|
| `-VmName` | `gitstack-test-vm` | Nome della VM in Hyper-V e base dell'hostname. |
| `-VmUser` | `gitstack` | Utente creato da cloud-init, sudo senza password. |
| `-SshPublicKey` / `-SshPublicKeyPath` | *(nessuno, uno dei due obbligatorio)* | Chiave pubblica SSH; mai una privata. |
| `-SwitchName` | `Default Switch` | Switch virtuale Hyper-V. |
| `-CpuCount` | `4` | vCPU. |
| `-MemoryGB` | `8` | RAM statica (dynamic memory disattivata). |
| `-DiskGB` | `60` | Disco; il filesystem lo riempie al primo avvio (growpart/resizefs). |
| `-VmPath` | `C:\HyperV-VMs\<VmName>` | Cartella con VHDX e ISO seed. Spazio libero verificato prima del download (vedi sopra). |
| `-UbuntuImageUrl` | vedi script | URL della cloud image `.img` (qcow2); verifica su [cloud-images.ubuntu.com/releases/noble/release](https://cloud-images.ubuntu.com/releases/noble/release/) se Canonical ha cambiato nome file. Lo SHA256 si verifica sempre contro `SHA256SUMS` della stessa cartella. |
| `-ImageCacheDir` | `%LOCALAPPDATA%\GitStack\test-vm\image-cache` | Cache dell'immagine scaricata. Spazio libero verificato prima del download. |
| `-QemuImgDockerImage` | `alpine:3.20@sha256:d9e8…4b6bc` | Immagine Docker (pinnata per digest) usata per la conversione qcow2 → VHDX. |
| `-CheckpointName` | `clean` | Nome dello checkpoint finale. |

`reset-vm.ps1` accetta `-VmName`, `-VmUser` (solo per stampare il comando
`ssh`), `-SwitchName` (deve combaciare con quello usato in `new-vm.ps1`, per
il fallback MAC → ARP), `-CheckpointName`, `-SshPrivateKeyPath` (facoltativo: stampa `ssh -i <percorso> utente@ip`). `remove-vm.ps1` accetta `-VmName` e `-Force` (salta
la conferma interattiva).

## Output atteso, passo per passo

`new-vm.ps1` stampa una riga `==>` per ogni fase e può richiedere diversi
minuti (soprattutto al primo download dell'immagine):

1. Controlli (amministratore, Hyper-V, client SSH, VM non già esistente,
   switch esistente, Docker pronto, spazio libero su `-ImageCacheDir` e `-VmPath`).
2. Download della cloud image (~600 MB, una volta sola: le esecuzioni
   successive riusano la cache se lo SHA256 combacia ancora) con verifica
   SHA256 contro `SHA256SUMS`.
3. Conversione del disco in VHDX dinamico (container Docker) ed espansione a `-DiskGB`.
4. Generazione del seed cloud-init (ISO `cidata`).
5. Creazione della VM, avvio, attesa IP (fino a `-BootTimeoutSeconds`, default 300s) e SSH (fino a `-SshTimeoutSeconds`, default 180s).
6. Attesa che `cloud-init status --wait` risponda sulla VM (fino a `-CloudInitTimeoutSeconds`, default 600s).
7. Spegnimento, checkpoint `clean`, riavvio, nuova attesa IP/SSH.
8. Blocco finale **"Cosa comunicare al team"**: nome VM, switch, utente, IP, specifiche (Ubuntu 24.04 LTS, vCPU/RAM/disco) e comando `ssh <utente>@<ip>` pronto da copiare.

`reset-vm.ps1` stampa: spegnimento, ripristino checkpoint, riavvio, attesa
IP/SSH, poi IP e comando `ssh` pronto (qualche minuto, molto più veloce di
`new-vm.ps1` perché non riscarica né riconverte nulla).

`remove-vm.ps1` chiede conferma (a meno di `-Force`), poi stampa spegnimento,
rimozione checkpoint, rimozione VM, eliminazione dei file.

### Rimozione dei file dopo `Remove-VM` (GIT-27)

Subito dopo `Remove-VM`, i processi Hyper-V (`vmms`/`vmwp`) possono tenere
ancora per qualche secondo il lock su VHDX, ISO seed e cartelle della VM
appena rimossa. `remove-vm.ps1` non ignora più l'errore in silenzio: per
ciascun percorso ritenta la rimozione (`Remove-Item`) fino a 30 secondi, ogni
3 secondi (funzione `Remove-ItemWithRetry` in `lib/common.ps1`), finché il
file non si sblocca.

**Due cartelle distinte, entrambe rimosse**: `New-VM -Path $VmPath` crea la
configurazione della VM in una sottocartella `<VmPath>\<VmName>`
(`$vm.ConfigurationLocation`), mentre VHDX e ISO seed generati da
`new-vm.ps1` vivono direttamente in `<VmPath>` (es. `<VmPath>\<VmName>.vhdx`).
Dopo aver eliminato i file e la sottocartella di configurazione,
`remove-vm.ps1` rimuove anche `<VmPath>` stessa se è rimasta vuota — è "la
cartella della VM" in senso stretto (quella passata a `-VmPath`, di default
`C:\HyperV-VMs\<VmName>`).

**Guardia contro la cancellazione di cartelle di altre VM**: una VM creata
*senza* `-Path` esplicito ha `ConfigurationLocation` uguale alla cartella
predefinita dell'host, **condivisa da tutte le VM**
(`(Get-VMHost).VirtualMachinePath`, tipicamente
`C:\ProgramData\Microsoft\Windows\Hyper-V`). Prima di cancellare una cartella
con `-Recurse`, `remove-vm.ps1` verifica con `Test-VmOwnedFolder`
(`lib/common.ps1`) che sia *dedicata solo a questa VM*: il suo nome finale
deve coincidere con `-VmName`, e il percorso non deve coincidere né essere un
antenato della cartella condivisa dell'host. Se la verifica fallisce, la
cartella **non** viene cancellata con `-Recurse`: si rimuove solo se già
vuota (come prima di GIT-27); se non è vuota resta sul posto e lo script
avvisa, elencando cosa contiene ancora, invece di rischiare di cancellare
configurazioni di altre VM.

Se alla fine resta qualcosa (VHDX, ISO, sottocartella di configurazione o
cartella `-VmPath`, per lock persistente o per la guardia sopra), lo script:

- stampa un avviso con l'elenco esatto dei percorsi rimasti e il comando
  `Remove-Item` da usare per toglierli a mano (dopo aver verificato il
  contenuto, se la guardia li ha lasciati per sicurezza);
- esce con codice **1** (la VM risulta comunque già rimossa da Hyper-V: solo
  i file/cartelle su disco sono rimasti).

In assenza di problemi, `remove-vm.ps1` esce con codice 0 e conferma che
VHDX, ISO e le due cartelle della VM sono stati eliminati.

### Se qualcosa non torna

- **`new-vm.ps1` si ferma su "la VM esiste già"**: è il comportamento atteso
  (idempotenza). Usa `reset-vm.ps1` o `remove-vm.ps1` prima di ricreare.
- **Timeout in attesa dell'IP**: prima si prova KVP (`Get-VMIntegrationService
  -VMName <nome>`, "Servizio di scambio dati" abilitato), poi il fallback MAC
  → ARP (vedi sopra). Se va in timeout dopo entrambi, apri la console della
  VM da Hyper-V Manager: se è al prompt di login ma non ha un IP, verifica
  che lo switch scelto distribuisca un IP via DHCP (`ip a` dentro la VM).
  Il messaggio di timeout riporta il MAC cercato, l'interfaccia host e la
  subnet: puoi confrontarli a mano con `Get-NetNeighbor -InterfaceAlias
  "vEthernet (<SwitchName>)"`.
- **Timeout in attesa di SSH / di cloud-init**: verifica che la chiave
  privata corrispondente a quella passata a `new-vm.ps1` sia caricata nel tuo
  `ssh-agent` o in un percorso predefinito di OpenSSH.
- **`Assert-DockerAvailable` fallisce ("docker non risponde" o "Windows
  containers")**: avvia Docker Desktop e attendi che il daemon sia pronto
  (icona nella tray); se è in modalità Windows containers, passa a Linux
  containers (tasto destro sull'icona → "Switch to Linux containers...").
- **La conversione Docker fallisce o produce un file vuoto**: se usi il
  backend Hyper-V di Docker Desktop (non WSL2), verifica la condivisione
  file del volume di `-ImageCacheDir`/`-VmPath` in *Settings → Resources →
  File sharing*. Verifica anche di aver passato percorsi Windows nativi
  (`F:\...`), non percorsi POSIX di una shell bash.
- **404/SHA256 non combacia sull'URL dell'immagine**: Canonical potrebbe aver
  cambiato nome file in `releases/noble/release/`. Verifica l'URL corrente e
  passa quello giusto con `-UbuntuImageUrl` (lo SHA256SUMS si scarica sempre
  dalla stessa cartella dell'URL passato).
- **"Spazio insufficiente su ..."**: il volume di `-ImageCacheDir` o
  `-VmPath` non ha abbastanza spazio libero (vedi soglie sopra). Passa un
  percorso su un volume più capiente, es. `-VmPath F:\HyperV-VMs\...
  -ImageCacheDir F:\HyperV-VMs\image-cache`.
- **La VM non si avvia per via del Secure Boot**: lo script disattiva già
  Secure Boot (`Set-VMFirmware -EnableSecureBoot Off`), necessario perché il
  template predefinito di Hyper-V è per Windows. Se serve riattivarlo, usa il
  template `MicrosoftUEFICertificateAuthority`.
- **`remove-vm.ps1` esce con codice 1 e avvisa che dei file/cartelle sono
  rimasti**: la VM è comunque già stata tolta da Hyper-V. Due cause
  possibili, distinguibili dal testo dell'avviso: (1) VHDX/ISO/cartella erano
  ancora bloccati da `vmms`/`vmwp` oltre i 30s di tentativi — aspetta qualche
  secondo che il processo che li blocca (di solito `vmwp.exe`, verificabile
  con `Get-Process vmwp`, o Hyper-V Manager ancora aperto su quella VM) li
  rilasci, poi lancia a mano il comando `Remove-Item` stampato dall'avviso;
  (2) la cartella non è risultata dimostrabilmente dedicata solo a questa VM
  (`Test-VmOwnedFolder`, vedi sopra) e non era vuota — verifica a mano cosa
  contiene (elencato nell'avviso) prima di cancellarla, potrebbe contenere
  file di un'altra VM. Rilanciare `remove-vm.ps1` non aiuta in nessuno dei
  due casi (la VM non esiste più, quindi non ricalcola quei percorsi).

## Come lo usano GIT-9 e GIT-11

1. Prima di ogni prova, lanciare `.\reset-vm.ps1`: riporta la VM al
   checkpoint `clean` (Ubuntu 24.04 LTS appena inizializzato da cloud-init,
   nessun altro software installato) e stampa IP e comando `ssh` pronti.
2. **GIT-9 (installer)**: eseguire l'installer via SSH sull'IP stampato da
   `reset-vm.ps1`, con l'utente `-VmUser` (default `gitstack`), che ha sudo
   senza password. I requisiti minimi verificati dall'installer in preflight
   (Ubuntu 24.04 LTS, 4 vCPU, 8 GB RAM, 60 GB disco) sono gli stessi valori
   predefiniti di `new-vm.ps1`.
3. **GIT-11 (end-to-end)**: il primo passo automatico dello script/workflow è
   sempre `.\reset-vm.ps1` (mai riuso di una VM già installata), poi verifica
   che la VM sia davvero pulita (nessun k3s/kubectl, nessuna cartella
   `/etc/rancher` o `/var/lib/rancher`, porte 80/443/6443 libere) prima di
   lanciare l'installer via SSH e i controlli end-to-end (UI, API, evento
   NATS).
4. **"Cosa comunicare al team"**: dopo la prima esecuzione di `new-vm.ps1` (o
   dopo un cambio di configurazione), il board gira a chi lavora su GIT-9/
   GIT-11 il blocco stampato a fine script: nome VM, switch, utente, IP e
   comando `ssh` — sono le informazioni minime per collegarsi e lanciare le
   prove.

## Test end-to-end (GIT-11)

`e2e.ps1` è il comando che il board lancia, da PowerShell **come
amministratore**, dalla radice di un checkout del ramo da provare, per la
prova end-to-end di M-01 (criteri di GIT-11). Fa tutto in un colpo: reset
della VM, controllo che sia davvero pulita, installer di GIT-9 dal commit
indicato di `main` (nel modo in cui lo userebbe il cliente, `curl | sudo
bash`), verifica di UI/API/evento JetStream e di identity attraverso il gateway, una seconda esecuzione
dell'installer per provare l'idempotenza, e la raccolta della diagnostica
dalla VM — sempre, anche se un passo fallisce.

Nessun agente ha accesso a Hyper-V o alla VM: prepara lo script, il board lo
esegue e incolla l'output nell'item.

### Prerequisiti aggiuntivi (oltre a quelli di `new-vm.ps1`/`reset-vm.ps1`)

- Una coppia di chiavi SSH **dedicata** a questo test (non quella personale
  del board): ed25519, **senza passphrase**, con la chiave pubblica già
  passata a `new-vm.ps1` (utente `-VmUser`, default `gitstack`) e la privata
  in `$env:USERPROFILE\.ssh\gitstack_vm`. Mai nel repository; `reset-vm.ps1`
  non la tocca.
- Il client `git` nel PATH (per risolvere lo SHA corrente di `origin/main`
  quando `-Ref` non è passato) e connessione a Internet (GitHub, ghcr.io).

### Comando

```powershell
.\deploy\test-vm\e2e.ps1
```

Parametri principali (tutti con un default sensato):

| Parametro | Default | Note |
|---|---|---|
| `-VmName` | `gitstack-test-vm` | Stessa VM di GIT-9/GIT-12. |
| `-VmUser` | `gitstack` | Utente SSH con sudo senza password. |
| `-KeyPath` | `$env:USERPROFILE\.ssh\gitstack_vm` | Chiave privata dedicata (vedi sopra). |
| `-GitStackRepo` | `fathorMB/GitStack` | `owner/repo` su GitHub e ghcr.io. |
| `-Ref` | SHA corrente di `origin/main`, risolto con `git ls-remote` e stampato | Commit da provare. |
| `-OutDir` | `%LOCALAPPDATA%\GitStack\e2e-runs\<timestamp>` | Log, diagnostica della VM e `known_hosts` isolato; a fine esecuzione anche `<OutDir>.zip`. |
| `-SkipReset` | (assente) | Salta il passo (a): riusa la VM nello stato attuale. Solo per il debug di questo script, mai per una prova valida. |
| `-JetStreamPollAttempts` / `-JetStreamPollIntervalSeconds` | `6` / `5` | Passo (e): letture del conteggio JetStream dopo la create, ogni N secondi, finché supera la baseline (fino a 30s in totale di default). |

### Output atteso, passo per passo

Una riga `[PASS] <passo>` o `[FAIL] <passo>: <motivo>` per ciascuno. In una
esecuzione verde sono **8 righe `[PASS]`** (7 nell'output reale di Atlas del 28/09, Ref
2819e60, prima del passo e2 di GIT-36), in quest'ordine:

```
[PASS] a. reset-vm.ps1 (checkpoint clean)
[PASS] b. macchina pulita (nessun k3s/kubectl, /etc/rancher, /var/lib/rancher, porte libere)
[PASS] d.1 immagini su ghcr.io per sha-<Ref>
[PASS] d.2 installer (prima esecuzione)
[PASS] c. requisiti minimi registrati nel log
[PASS] e. UI, /api/healthz, create+read risorsa di prova, evento JetStream
[PASS] e2. identity via gateway: sessione assente 401 unauthenticated, login inventato 401 invalid_credentials, /internal non esposto
[PASS] f. idempotenza (seconda esecuzione, k3s non reinstallato, password invariata, healthz OK)
RISULTATO: VERDE
```

Cosa controlla ciascun passo:

- **a.** `reset-vm.ps1` (checkpoint `clean`) e IP della VM.
- **b.** macchina pulita: nessun `k3s`/`kubectl`, nessun servizio `k3s`,
  niente `/etc/rancher` o `/var/lib/rancher`, porte 80/443/6443 libere. Se
  fallisce, lo script si ferma: non installa nulla.
- **d.1** le quattro immagini (`gitstack-gateway`, `gitstack-identity`,
  `gitstack-core`, `gitstack-web`) esistono su ghcr.io con tag `sha-<Ref>` (pubblicate dal
  job `registry` della CI dopo un push su `main`).
- **d.2** prima esecuzione dell'installer via SSH (`installer-run1.log`).
- **c.** le righe di preflight (OS, architettura, CPU, RAM, disco) sono
  presenti nel log della prima esecuzione.
- **e.** UI (`http://<ip>/`, dall'host Windows) -> 200 con l'HTML della web
  UI; `/api/healthz` -> 200 `{"status":"ok",...}`. La risorsa di prova e
  l'evento JetStream sono nel passo **e4** (dal GIT-54 il gateway richiede
  l'autenticazione anche per `/api/v1/resources`).
- **e2.** identity attraverso il gateway (GIT-36), dall'host Windows e senza
  credenziali valide: `GET /api/v1/auth/session` senza cookie -> 401 con
  code `unauthenticated`; `POST /api/v1/auth/login` con credenziali
  inventate -> 401 con code `invalid_credentials` (risposte di identity, non
  del gateway, che darebbe 404/503); `POST /api/v1/internal/verify` -> 404
  (interfaccia interna non esposta). Il login con l'admin e una chiamata
  autenticata si aggiungono con GIT-35 (admin al primo avvio).
- **e4.** risorsa di prova (GIT-54): `GET /api/v1/resources` senza
  credenziali -> 401 `unauthenticated`; create + read via `/api/v1/resources`
  con la sessione dell'admin (password gia cambiata nel passo e3); i messaggi
  dello stream JetStream `CORE` aumentano dopo la create (letti dalla VM con
  un pod `natsio/nats-box` pinnato, senza attach: pod creato, atteso
  `Succeeded`, JSON preso da `kubectl logs` solo su stdout, pod sempre
  cancellato). `core` pubblica l'evento in una goroutine **dopo** il 201
  (timeout 5s), quindi il conteggio e' un polling
  (`-JetStreamPollAttempts` tentativi ogni `-JetStreamPollIntervalSeconds`,
  default 6x5s), e ogni tentativo e' scritto nel log.
- **f.** idempotenza: seconda esecuzione (`installer-run2.log`) -> exit 0,
  hash della password di Postgres invariato, `ActiveEnterTimestamp` di
  `k3s` invariato, `/api/healthz` ancora 200.
- **g.** (sempre, anche dopo un fallimento) diagnostica raccolta dalla VM
  (`journalctl -u k3s`, `kubectl get all -A`, describe/log dei pod, eventi,
  `helm status`, `df`/`free`) in `<OutDir>\vm-diagnostics\`.

Esiste anche una riga `[FAIL] preparazione: copia di .../remote.sh sulla
VM`, ma solo se ssh/scp verso la VM non funziona: in un'esecuzione verde
non compare.

Alla fine: il riepilogo, il percorso dello zip `<OutDir>.zip` (log +
diagnostica, **sempre creato**) e `RISULTATO: VERDE` o `RISULTATO: ROSSO`.
Il codice di uscita e' diverso da zero se un passo e' fallito.

### Cosa mandare al team

L'output completo della console **e** il file `<OutDir>.zip`: contiene
`e2e.log` (tutto quello stampato a schermo), `installer-run1.log`,
`installer-run2.log`, `reset-vm.log` e `vm-diagnostics\` (o una nota se la
diagnostica non è stata raccolta, es. VM irraggiungibile via SSH). Un'
esecuzione con tutti i passi `[PASS]` sul commit di `main`, con questo zip
allegato all'item, è il criterio "Verde sul ramo principale" di GIT-11.

## Limiti noti e verifiche fatte in questa sessione (GIT-25)

L'ambiente di questa sessione ha Hyper-V raggiungibile, diritti di
amministratore e uno switch `Default Switch` attivo, quindi è stato
possibile provare per davvero i pezzi più a rischio della correzione
(download, hash, conversione, calcolo di subnet/ARP) con dati reali, non
solo a livello di sintassi. **Non è stata creata una VM end-to-end**
(nessuna chiave SSH del board disponibile in sessione, e creare/avviare una
VM reale su quella che sembra la macchina di lavoro del board avrebbe
lasciato artefatti Hyper-V da ripulire a mano): quella è la prova che farà
Atlas lanciando `new-vm.ps1` e poi `e2e.ps1` dopo il reintegro, come da
piano del CTO.

Verifiche fatte per davvero in questa sessione:

- **URL e SHA256 (criterio 1)**: `curl -I` sul nuovo URL predefinito →
  `HTTP/1.1 200 OK` (Content-Length 625612288, cioè 596 MiB); sul vecchio
  URL (`releases/24.04/release/...vhd.tar.gz`) → `302` verso
  `releases/noble/release/...`, poi `404` (conferma la diagnosi). Scaricato
  `SHA256SUMS` reale e verificato che `Get-Sha256SumsExpectedHash` estrae
  `6a81c37564db9b1ee84e141922625e1d7c5b389b99bb3c572e0243607d5bb4d2` per
  `ubuntu-24.04-server-cloudimg-amd64.img`, uguale all'hash citato dal CTO
  nel piano dell'item. **Bug trovato e corretto durante questa verifica**:
  `Invoke-WebRequest -UseBasicParsing` su PowerShell 5.1 ritorna `.Content`
  come `byte[]` (non stringa) quando il server risponde con
  `Content-Type: application/octet-stream` (il caso di `SHA256SUMS`); senza
  decodifica esplicita (`[System.Text.Encoding]::UTF8.GetString`) lo split
  per riga falliva silenziosamente. Vedi `Get-Sha256SumsExpectedHash` in
  `lib/common.ps1`.
- **Download reale**: `Get-CloudImageWithHashVerification` ha scaricato
  l'immagine vera (596 MiB, via `curl.exe`) in ~14s, verificato lo SHA256, e
  al secondo giro ha riusato la cache senza riscaricare (hash combaciante,
  confermato dal log `"Uso l'immagine gia' in cache"`).
- **Conversione qcow2 → VHDX dinamico (criterio 2)**: `Convert-QcowToVhdxDynamic`
  ha convertito l'immagine reale con il container `alpine:3.20@sha256:d9e8…4b6bc`
  in ~27s; `Get-VHD` sul risultato conferma `VhdFormat: VHDX`, `VhdType:
  Dynamic`, `FileSize` (2.04 GiB) molto minore di `Size` (3.5 GiB, sparse).
  `Resize-VHD` a 60 GB provato sullo stesso file: `Size` sale a 60 GB,
  `FileSize` resta invariato (dinamico, non pre-allocato). Confermato che
  Docker Desktop (backend WSL2 su questa macchina) accetta bind mount con
  percorso Windows nativo (`F:\...:/src-in:ro`) ma **non** un percorso POSIX
  di bash (`/f/...`, mount vuoto/fallito): è il limite documentato sopra.
- **MAC → ARP (criterio 3)**: `ConvertTo-NormalizedMacAddress` provata su
  input con/senza trattini. `Get-Ipv4SweepTargets` provata sull'interfaccia
  reale `vEthernet (Default Switch)` di questa macchina: subnet calcolata
  `172.20.144.0/20` (prefix 20, 4094 host) — cambia a ogni riavvio
  dell'host, confermando perché non va codificata. `Invoke-Ipv4ArpSweep`
  (stesso meccanismo, async `Ping`) provata su una subnet /24 reale (Wi-Fi
  di questa macchina): 254 ping asincroni in 517ms, cache ARP passata da 8 a
  13 voci; su una subnet equivalente a un /20 (4064 indirizzi): 2.7s totali.
  **Non provato**: la corrispondenza end-to-end MAC di una VM reale → voce
  ARP → conferma su `:22`, perché non è stata creata una VM in questa
  sessione (nessun MAC Hyper-V reale da cercare).
- **Spazio libero (criterio 5)**: `Assert-FreeSpace` provata sia sul volume
  `C:` di questa macchina (19.9 GB liberi) con una soglia di 65 GB → `FAIL`
  chiaro con GB liberi/richiesti, sia su `F:` (649.4 GB liberi) con la
  stessa soglia → passa. Riproduce esattamente lo scenario diagnosticato
  dal board su GIT-25.
- **Sintassi PowerShell (criterio 6)**: `new-vm.ps1`, `reset-vm.ps1`,
  `remove-vm.ps1`, `lib/common.ps1` analizzati senza errori con
  `[System.Management.Automation.Language.Parser]::ParseFile`.
  PSScriptAnalyzer non è disponibile in questo ambiente.
- **cloud-init invariato (criterio 4)**: `user-data.yaml.tmpl` non toccato
  da questo item; confermato a mano `package_upgrade:
  false` (la chiave `packages` non c'è più dopo GIT-28: nessun pacchetto), `ssh_pwauth: false`, `lock_passwd: true`, nessun `passwd:`/hash.
- **Generazione dell'ISO seed `cidata`**: `New-CloudInitSeedIso` (non
  toccata da GIT-25) non ri-verificata in questa sessione oltre al parser;
  era già stata provata isolatamente in una sessione precedente (GIT-12).

Non provato per limiti di questa sessione (dichiarato, non nascosto):

- Boot reale della VM, cloud-init NoCloud sull'immagine `.img` generica,
  KVP effettivamente assente (dedotto dal manifest citato nel piano del
  CTO, non verificato leggendo `/var/log/cloud-init.log` di una VM vera).
- Risoluzione MAC → ARP contro un MAC Hyper-V reale e conferma su `:22`
  contro una VM reale.
- `e2e.ps1` di GIT-11: letto il codice sul ramo `item/GIT-11`
  (`git show item/GIT-11:deploy/test-vm/e2e.ps1`) per confermare come
  ricava l'IP — vedi sotto — ma non eseguito (richiede l'installer di GIT-9
  e una VM reale).

### Come `e2e.ps1` (GIT-11) ricava l'IP, verificato leggendo il codice

`e2e.ps1` (passo *a*, `deploy/test-vm/e2e.ps1` sul ramo `item/GIT-11`) usa
due percorsi, **nessuno dei due richiede modifiche a GIT-11**:

- Normale (senza `-SkipReset`): lancia `reset-vm.ps1` come processo figlio
  e legge l'IP dal suo output con la regex `IP:\s*(\d{1,3}(?:\.\d{1,3}){3})`
  — la riga `Write-Host "IP:  $ip"` di `reset-vm.ps1` non è stata cambiata
  da GIT-25, solo il modo in cui `$ip` viene calcolato internamente.
- Con `-SkipReset` (solo debug): chiama `Wait-VmIPv4Address -VmName $VmName
  -TimeoutSeconds $ResetBootTimeoutSeconds` senza `-SwitchName`, quindi
  eredita il default `'Default Switch'` della nuova firma — funziona senza
  toccare il ramo `item/GIT-11`, a patto che la VM sia sul `Default Switch`
  (il default anche di `new-vm.ps1`).

### Test di `Invoke-HttpRaw` (GIT-58)

`Invoke-HttpRaw` sta in `lib/http.ps1` (dot-sourced da `e2e.ps1`). Su Windows
PowerShell 5.1 `Invoke-WebRequest -Headers @{ Cookie = ... }` non manda il
cookie, quindi si usa `-Cookie 'gst_session=<valore>'`, che costruisce una
`WebRequestSession` con un cookie non Secure sull'host dell'URI. Il corpo delle
risposte 4xx/5xx si legge da `$_.ErrorDetails.Message` e, se vuoto, dallo stream
riportato a Position 0. `tests/http.Tests.ps1` lo prova con un `HttpListener` su
127.0.0.1 (401 con corpo JSON che rimanda il Cookie ricevuto); esce con codice
!= 0 se fallisce:

```powershell
powershell.exe -NoProfile -File deploy\test-vm\tests\http.Tests.ps1
```
