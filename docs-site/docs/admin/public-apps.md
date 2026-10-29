---
id: public-apps
title: Public apps (administrator guide)
---

By default an app hosted by GitStack is reachable only from inside the
installation, behind the GitStack login. As the installation administrator you
can let repository admins publish an app **on the internet**, with its own
domain and a Let's Encrypt certificate. This page covers what you have to
switch on and open. The person who publishes the app follows
[Publish an app on your own domain](../user/publish-an-app.md).

## Two consents

An app becomes public only when **both** agree:

1. you, the installation administrator, turn on **Public apps** (this page);
2. the `admin` of the repository chooses the access mode `public` for that app.

Nothing is public until both have happened, and turning your switch off takes
every public app off the internet at once.

## Before you start

- App hosting is on (`gitstack status` prints `Hosting delle app: acceso`). If
  not, see [Install](./install.md) and run
  `sudo gitstack config set apps on` (add `--apps-domain apps.example.com` if
  the installation has no apps domain yet).
- You are an administrator of the installation: the settings below answer
  `403 forbidden` to anyone else.

## 1. Open port 80 (and 443)

Let's Encrypt proves that you control a host with the **HTTP-01** challenge: it
connects to your server on **port 80** from the internet. Forward ports 80 and
443 from your router or firewall to the GitStack machine. Traefik keeps
redirecting port 80 to HTTPS, except the challenge path and
`/downloads/ca.crt`.

The console checks the port for you: **Administration → Apps & resources →
Instance settings → Let's Encrypt** shows *Port 80: reachable / not reachable /
not checked*, with the time of the last check and the reason when it fails. The
check connects to the public IPs you declared (step 3) and repeats every 10
minutes.

## 2. Set the ACME e-mail

Let's Encrypt is **off** until the installation has an ACME account e-mail. Two
things are involved:

- The Traefik resolver (`letsencrypt`, HTTP-01) is written by the installer. It
  exists when you installed with `--tls letsencrypt`, or with the apps on and
  `--tls-email`:

  ```sh
  sudo ./deploy/install.sh --host git.example.com --apps-domain apps.example.com \
    --enable-apps --tls-email admin@example.com
  ```

  An installation made without an e-mail has no resolver: run the installer
  again with `--tls-email` (it keeps the choices you made before).
- The e-mail is also shown and saved in the console: **Instance settings →
  Let's Encrypt → ACME email → Save email**. Let's Encrypt uses it to warn you
  about expiring certificates.

The *Let's Encrypt* line shows **active** when an e-mail is set and public apps
are on.

## 3. Declare the public IPs

For a domain at the apex (`retail.it`, where a CNAME is not allowed) the DNS
check compares the `A`/`AAAA` records with the **public IPs of the installation
that you declare**. Without them only the CNAME form is offered. The port 80
check uses the same list. Set them in the console, in **Administration → Apps &
resources → Instance settings → Public IPs**: add or remove each address and
press *Save public IPs*. The same list can be set with the API (only public,
non-private addresses are accepted, otherwise `422`, which the console shows as
an error and keeps the list you were editing). `PUT /admin/apps/settings`
replaces all required fields, so read the current ones first:

```sh
gs api /admin/apps/settings
gs api -X PUT /admin/apps/settings --input settings.json
```

where `settings.json` is the answer of the first command (without the read-only
fields `appsDomain`, `appsDomainIsDefault` and `letsEncrypt`) plus
`"publicIps": ["203.0.113.10"]`. An empty list removes them.

## 4. Turn on Public apps

**Administration → Apps & resources → Instance settings → Public apps**, switch
on and confirm. The same page lists every public app of the installation with
its domains and certificate state (*no certificate*, *issuing*, *valid*,
*error*).

## Let's Encrypt limits

- **Public address of each public app.** Besides custom domains, the automatic
  address `<app>-<repo>-<owner>.<apps-domain>` of a public app gets its own
  Let's Encrypt certificate (not the internal CA, which visitors from the
  internet do not trust). The apps domain must therefore be a public domain
  whose DNS points to this machine, otherwise Let's Encrypt cannot issue.
- **Rate limits.** Let's Encrypt limits how many certificates it issues per
  registered domain and per identical set of names in a week, and how many
  failed validations it accepts. They are set by Let's Encrypt and change over
  time: see
  [letsencrypt.org/docs/rate-limits](https://letsencrypt.org/docs/rate-limits/).
  Many public apps under one apps domain count against the same registered
  domain.
- **No wildcard.** HTTP-01 cannot issue wildcard certificates: each host gets
  its own certificate.
- **Fallback.** If Let's Encrypt cannot be used (no ACME e-mail, or port 80
  unreachable) a public app is still served, with the internal CA certificate
  and a warning in the console. Visitors will see a certificate error until
  you fix it.

## What happens when you turn it off

Switch **Public apps** off and confirm:

- public apps stop being reachable from the internet: their routes (automatic
  address and custom domains) are removed, **without stopping or restarting
  the apps**;
- the apps keep running and stay reachable **inside the installation**, behind
  the GitStack login (as with the `login` mode), at their automatic address.
  If the GitStack login for apps is not configured, or the switch cannot be
  read, they have no route at all: the apps are never exposed by mistake;
- every app keeps its `public` choice, its custom domains and its
  certificates, uploaded or issued. Turning the switch on again brings
  everything back without anyone redoing the setup;
- while it is off, choosing `public` answers `422 public_apps_disabled`,
  domains cannot be added, DNS is not rechecked and certificate renewal is
  suspended until you turn it on.

## Related

- [HTTPS and certificates](./https-and-ca.md): the certificate of GitStack
  itself and the internal CA.
- [Publish an app on your own domain](../user/publish-an-app.md): the guide for
  repository admins.
