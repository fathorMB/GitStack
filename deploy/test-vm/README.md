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
| `reset-vm.ps1` | Riporta la VM al checkpoint `clean`, la riavvia e stampa il suo IP. Da lanciare prima di ogni prova di GIT-9/GIT-11. |
| `remove-vm.ps1` | Rimuove la VM, i suoi checkpoint e i file (VHDX, ISO seed). Chiede conferma a meno di `-Force`. |
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
  (~600 MB, messa in cache locale dopo il primo download).
- Uno switch virtuale Hyper-V esistente (di default `Default Switch`, sempre
  presente quando Hyper-V è attivo).

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
| `-VmPath` | `C:\HyperV-VMs\<VmName>` | Cartella con VHDX e ISO seed. |
| `-UbuntuImageUrl` | vedi script | URL della cloud image `.vhd.tar.gz`; verifica su [cloud-images.ubuntu.com/releases/24.04/release](https://cloud-images.ubuntu.com/releases/24.04/release/) se Canonical ha cambiato nome file. |
| `-CheckpointName` | `clean` | Nome dello checkpoint finale. |

`reset-vm.ps1` accetta `-VmName`, `-VmUser` (solo per stampare il comando
`ssh`), `-CheckpointName`. `remove-vm.ps1` accetta `-VmName` e `-Force` (salta
la conferma interattiva).

## Output atteso, passo per passo

`new-vm.ps1` stampa una riga `==>` per ogni fase e può richiedere diversi
minuti (soprattutto al primo download dell'immagine):

1. Controlli (amministratore, Hyper-V, client SSH, VM non già esistente, switch esistente).
2. Download della cloud image (~600 MB, una volta sola: le esecuzioni successive riusano la cache) ed estrazione.
3. Conversione del disco in VHDX ed espansione a `-DiskGB`.
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

### Se qualcosa non torna

- **`new-vm.ps1` si ferma su "la VM esiste già"**: è il comportamento atteso
  (idempotenza). Usa `reset-vm.ps1` o `remove-vm.ps1` prima di ricreare.
- **Timeout in attesa dell'IP**: l'IP viene letto dai servizi di integrazione
  Hyper-V (scambio dati/KVP) sull'adattatore di rete della VM. Apri la console
  della VM da Hyper-V Manager: se è al prompt di login, verifica `Get-VMIntegrationService -VMName <nome>` (il servizio "Servizio di scambio dati" deve essere abilitato) e che lo switch scelto distribuisca un IP via DHCP.
- **Timeout in attesa di SSH / di cloud-init**: verifica che la chiave
  privata corrispondente a quella passata a `new-vm.ps1` sia caricata nel tuo
  `ssh-agent` o in un percorso predefinito di OpenSSH.
- **`Convert-VHD` fallisce o la VM non si avvia (Gen2 richiede GPT/UEFI)**:
  Canonical potrebbe aver cambiato formato/nome del file su
  cloud-images.ubuntu.com. Verifica l'URL corrente e passa quello giusto con
  `-UbuntuImageUrl`; in alternativa converti l'immagine `.img` (qcow2) con
  `qemu-img convert -O vhdx` prima di adattare lo script.
- **La VM non si avvia per via del Secure Boot**: lo script disattiva già
  Secure Boot (`Set-VMFirmware -EnableSecureBoot Off`), necessario perché il
  template predefinito di Hyper-V è per Windows. Se serve riattivarlo, usa il
  template `MicrosoftUEFICertificateAuthority`.

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

## Limiti noti e verifiche fatte in questa sessione

Questi script eseguono comandi Hyper-V (`New-VM`, `Convert-VHD`,
`Checkpoint-VM`, ecc.) che richiedono Hyper-V attivo e diritti di
amministratore sulla macchina Windows del board: **non sono eseguibili né
testabili end-to-end nell'ambiente di lavoro di questa sessione**. La prova
vera la fa il board lanciando `new-vm.ps1` una volta sulla propria macchina.

Verifiche fatte in sessione, senza Hyper-V:

- **Sintassi PowerShell**: ogni script (`new-vm.ps1`, `reset-vm.ps1`,
  `remove-vm.ps1`, `lib/common.ps1`) analizzato senza errori con il parser di
  PowerShell (`[System.Management.Automation.Language.Parser]::ParseFile`).
  PSScriptAnalyzer non è disponibile in questo ambiente.
- **YAML dei template cloud-init**: `user-data.yaml.tmpl` e
  `meta-data.yaml.tmpl`, con i segnaposto `{{...}}` sostituiti da valori di
  prova, validati con PyYAML (`yaml.safe_load`) — YAML valido.
- **Generazione dell'ISO seed `cidata`**: la funzione `New-CloudInitSeedIso`
  di `lib/common.ps1` (COM IMAPI2FS + lettore `IStream` via .NET, nessuno
  strumento esterno) è stata provata isolatamente su questa macchina Windows
  (senza Hyper-V) con contenuti di prova: produce un file `.iso` non vuoto.
- Verificato a mano che il template `user-data.yaml.tmpl` non contiene
  `passwd:`/hash di password, ha `ssh_pwauth: false`, `lock_passwd: true`,
  `packages: []` e `package_upgrade: false`.
