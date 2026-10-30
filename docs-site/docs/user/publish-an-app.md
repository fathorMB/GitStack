---
id: publish-an-app
title: Publish an app on your own domain
---

An app hosted by GitStack opens, by default, only for people who can read the
repository, behind the GitStack login. If you have the **admin** role on the
repository you can make an app **public**: reachable from the internet at its
automatic address and, if you wish, at a domain of your own such as
`shop.retail.it`, with an HTTPS certificate.

:::warning The app must handle authentication itself
A public app has **no GitStack login in front of it**. Anyone on the internet
can open it, and it receives **no** `X-GitStack-*` headers: it does not know
who the visitor is, and any such header sent by a client is discarded. Login,
sessions and authorization for your visitors are the app's job. Do not publish
an app that relies on the GitStack login to protect its data.
:::

## Prerequisites

- You have `admin` on the repository.
- The installation administrator has turned on **Public apps**. If not, the
  option is disabled and asks for it; they follow
  [Public apps (administrator guide)](../admin/public-apps.md).
- The commands below use the [`gs` CLI](../cli/index.md); add
  `-R owner/repo` outside a clone. The examples publish an app called `sito`.

## 1. Make the app public

In the console: app → **Settings → Access & domains → Public on the
internet**, then confirm. Or:

```sh
gs app access sito --mode public
```

Publishing asks you to type the app name to confirm (with `--yes` instead when
there is no terminal). The command prints the app's automatic address. To go
back, `gs app access sito --mode login`.

## 2. Add your domain

```sh
gs app domain add sito shop.retail.it
```

The domain starts as `pending_dns` and the command prints the DNS record to
create. Domains that cannot be used: the GitStack domain, the apps domain and
its sub-domains, IP addresses (`422`), and a domain already used by another app
(`409`).

## 3. Create the DNS record

At your DNS provider create the record printed by the command (also in
**Access & domains**, and with `gs app domain list sito`):

| Domain | Record |
|---|---|
| Sub-domain (`shop.retail.it`) | `CNAME` to the automatic address of the app, `<app>-<repo>-<owner>.<apps-domain>` |
| Apex (`retail.it`), where a CNAME is not allowed | `A` / `AAAA` to a public IP of the installation, **only** if the administrator has declared one; the output lists it as an alternative |

If the domain already has other `A`/`AAAA` records, remove them: for the
address form, the domain must resolve **only** to the declared IPs.

## 4. Verify

GitStack checks the DNS by itself (every 5 minutes at first, then less often).
To check right now:

```sh
gs app domain verify sito shop.retail.it
```

The command exits 0 even when DNS has not answered yet; read the status:

| Status | Meaning |
|---|---|
| `pending_dns` | no record found yet (DNS can take time to propagate) |
| `error` | records exist but differ; the reason says what was found |
| `verified` | the DNS is right: the route and the certificate start from here |

A verified domain whose DNS later stops matching keeps working for one hour,
then goes to `error` and loses its route.

## 5. Certificate

Once `verified`, GitStack asks **Let's Encrypt** for a certificate and renews
it automatically. The state in `gs app domain list sito` goes
`none → issuing → valid`, or `error` with the reason. It needs port 80 of the
installation to be reachable from the internet, which is the administrator's
side.

To use **your own certificate** instead (for example one issued by your
company), upload a PEM chain (leaf first) and the private key (PEM, no
passphrase):

```sh
gs app domain cert upload sito shop.retail.it --cert fullchain.pem --key privkey.pem
```

GitStack checks that the key matches, that the certificate covers the domain
and that it is not expired (`422` otherwise). The key is never printed or read
back. From then on **you** renew it: upload the new pair before it expires.
To return to Let's Encrypt:

```sh
gs app domain cert rm sito shop.retail.it
```

## Remove a domain, or the public access

```sh
gs app domain rm sito shop.retail.it
gs app access sito --mode login
```

If the administrator turns **Public apps** off, your app keeps running and is
no longer reachable from the internet (the automatic public address and your
custom domains are removed). Inside the installation it stays reachable at its
usual address, but behind the GitStack login, as if its mode were `login`. It
keeps its `public` choice, domains and certificates; the public routes and the
domains come back by themselves when the switch is turned on again, with
nothing for you to do.
