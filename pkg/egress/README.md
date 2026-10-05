# pkg/egress

Client HTTP con protezione SSRF per le chiamate in uscita verso indirizzi
dati dagli utenti (webhook di M-06, regola C8, e ogni futura chiamata
analoga). Solo libreria standard.

```go
policy, err := egress.NewPolicy(egress.Config{
    Allow:        cfg.Allow,        // egress.allow del chart
    Deny:         cfg.Deny,         // egress.deny
    ClusterCIDRs: cfg.ClusterCIDRs, // egress.clusterCIDRs (default k3s)
})
client := egress.NewClient(policy, egress.Options{})
resp, err := client.Post(url, "application/json", body)
if egress.IsBlocked(err) { /* destinazione vietata: non ritentare */ }
snippet, truncated, _ := egress.ReadBody(resp.Body) // max 4 KB per il log
```

Ogni chiamata utente in uscita deve passare da questo client: non usare
`http.DefaultClient` né un `http.Transport` proprio.

## Modello di minaccia

**Attaccante**: un utente che può configurare l'URL di un webhook (o un
server che l'utente controlla, che risponde con redirect o cambia i record
DNS). **Obiettivo**: far fare a GitStack richieste verso servizi interni che
l'utente non può raggiungere: l'API di metadati cloud (169.254.169.254), i
servizi del cluster k3s (core, identity, NATS, Postgres), i servizi in
ascolto su localhost del nodo.

| Attacco | Difesa |
|---------|--------|
| URL con IP letterale (`http://127.0.0.1`, `[::1]`, `0.0.0.0`) | il controllo avviene sull'IP, non sul testo dell'URL |
| Forme alternative (`::ffff:127.0.0.1`, `fe80::1%eth0`, NAT64 `64:ff9b::7f00:1`) | l'indirizzo è normalizzato (unmap IPv4-mapped, zona rimossa, IPv4 incorporato in NAT64) prima del controllo |
| Nome che risolve in un IP vietato | si controlla l'IP risolto, mai il nome |
| DNS rebinding (risposta valida al controllo, vietata alla connessione) | il controllo è anche nel `Control` del `net.Dialer`, eseguito sull'indirizzo effettivo subito prima di `connect(2)`: non c'è TOCTOU |
| Redirect verso un indirizzo vietato | ogni hop apre una nuova connessione, quindi passa dal dialer; `CheckRedirect` limita a 5 hop e ammette solo `http`/`https` |
| Proxy da variabile d'ambiente | `Transport.Proxy` è nil: la destinazione dialata sarebbe il proxy, non l'URL |
| Risposta enorme nel log | `ReadBody` legge al massimo 4 KB (`MaxLoggedBody`) e segnala il troncamento |
| Richiesta lenta | timeout totale di 10 s (configurabile in `Options`) |

**Fuori ambito**: il contenuto del body e degli header inviati (è
responsabilità del chiamante), la validazione del certificato TLS (resta
quella standard di Go), i servizi esposti su indirizzi pubblici ammessi.

## Reti bloccate

Sempre, senza che nessuna lista dell'amministratore possa sbloccarle:

- loopback `127.0.0.0/8`, `::1`;
- link-local `169.254.0.0/16`, `fe80::/10` (e site-local `fec0::/10`);
- `0.0.0.0/8`, `::/96` (non specificato, IPv4-compatible);
- multicast `224.0.0.0/4`, `ff00::/8`;
- `240.0.0.0/4` (riservato, include il broadcast `255.255.255.255`);
- i CIDR del cluster (`clusterCIDRs`; default k3s: pod `10.42.0.0/16`,
  servizi `10.43.0.0/16`). Un elenco vuoto torna ai default.

Per default, ma sbloccabili con una voce IP/CIDR in `allow` (reti speciali,
usate p.es. da Tailscale o da laboratori):

- `100.64.0.0/10` (CGNAT), `192.0.0.0/24`, `192.0.2.0/24`, `198.51.100.0/24`,
  `203.0.113.0/24` (documentazione), `198.18.0.0/15` (benchmark),
  `192.88.99.0/24`;
- `2001::/32` (Teredo), `2002::/16` (6to4), `2001:db8::/32`, `100::/64`.

Ammessi: indirizzi pubblici e la rete aziendale (RFC 1918 `10/8`,
`172.16/12`, `192.168/16`, ULA `fc00::/7`) fuori dal cluster.

## Liste dell'amministratore

Impostate nei values del chart (`egress.allow`, `egress.deny`,
`egress.clusterCIDRs`). Voci: IP, CIDR, nome host (`hooks.example.com`) o
suffisso (`*.example.com`, non coincide col dominio nudo). Il collegamento
alle variabili d'ambiente del servizio è di M-06/G.

Ordine di valutazione, sull'IP effettivo (e sul nome se la voce è un nome):

1. blocchi permanenti e `clusterCIDRs`: rifiutano sempre;
2. `deny`: rifiuta (vince su `allow`);
3. reti speciali: rifiutano, salvo una voce IP/CIDR in `allow` che le copre;
4. se `allow` non è vuota (**restringe**): passa solo ciò che una sua voce
   copre (per IP/CIDR o per nome); altrimenti passa tutto il resto.

Quindi `deny` restringe, `allow` vuota non cambia nulla, `allow` non vuota
restringe a un elenco chiuso e allarga solo le reti speciali. Un `allow` non
sblocca mai loopback, link-local o cluster, nemmeno `0.0.0.0/0`. Un nome in
`deny` è rifiutato prima della risoluzione DNS.

Voci non valide fanno fallire `NewPolicy` all'avvio.

## Test

`go test ./... -count=1`. I test usano `httptest` su 127.0.0.1 (che deve
risultare bloccato) e, per provare un indirizzo ammesso, un resolver finto e
una connessione deviata verso il server di prova (hook non esportati in
`Options`), senza disattivare la protezione.
