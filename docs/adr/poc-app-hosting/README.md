# Prova ADR 0002: app interna dietro Traefik

La prova crea un cluster k3d effimero, una CA privata e un certificato wildcard per
`*.apps.test`. Un `TLSStore` `default` nel namespace di Traefik conserva il Secret TLS;
le IngressRoute usano `tls: {}` e non copiano la chiave nei namespace delle app. Nel namespace
`gitstack-app-poc` installa `whoami`; il forward-auth gira separatamente in `gitstack-poc`.

Non è codice di prodotto: rende osservabile la scelta dell'ADR 0002. CA e chiavi sono
temporanee e rimangono nella directory temporanea dello script.

Prerequisiti: Docker, `k3d` v5.9.0, `kubectl`, `openssl` e `curl`.

```sh
./run.sh
```

Lo script distrugge solo il cluster `gitstack-app-poc` che ha creato (imposta
`K3D_CLUSTER` per un nome alternativo). L'esito atteso contiene:

- `OK: TLS CA interna e forward-auth autorizzato`
- `OK: header del client sostituiti`
- `OK: forward-auth rifiuta senza sessione`
- `ESITO: OK`

La prova usa un kubeconfig temporaneo e non modifica quello attivo. `K3D_BIN` e
`KUBECTL_BIN` permettono di indicare i binari quando `bash` e Docker Desktop appartengono
a Windows ma la shell è WSL. `APP_HTTPS_PORT` cambia la porta locale predefinita 18443 se
è già occupata.
