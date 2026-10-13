---
id: webhooks
title: Webhooks
---

A webhook sends an HTTP `POST` to your address whenever a chosen event
happens. Create one on a repository (**admin** role, page **Webhooks** of the
repository) or on an organization (**owner** role, for all its repositories).

The format is GitStack's own, versioned (currently version 1) and inspired by
GitHub's, not compatible with it: a receiver written for GitHub needs
adapting.

## Events

| Event (`X-GitStack-Event`) | When |
|---|---|
| `push` | after each accepted push, one message per branch or tag |
| `issues` | an issue is opened, edited, closed, reopened, assigned, labeled, milestoned or locked |
| `issue_comment` | a comment is created, edited or deleted |
| `repository` | a repository is created, deleted, restored, archived or its visibility changes |

Hidden issues produce no webhook. There is no `ping` event: test a webhook
with a real delivery and use **Redeliver** from the log.

## Headers and signature

Each request carries `X-GitStack-Event`, `X-GitStack-Delivery` (the id in the
log), `X-GitStack-Payload-Version` and, if you set a secret,
`X-GitStack-Signature: sha256=<hex>`. The signature is the HMAC-SHA256 of the
exact request body with your secret as key. Recompute it on the raw body and
compare in constant time. **Always set a secret** on addresses reachable from
outside. The secret is never returned by the API.

## Delivery

- A `2xx` answer within 10 seconds is a success. A `5xx`, a network error or
  a timeout is retried up to 8 times in about 24 hours; a `4xx` other than
  `410` is not retried, and `410 Gone` stops the delivery.
- After 3 days of consecutive failures the webhook is deactivated and its
  creator is notified; reactivate it from the page or with the API.
- The delivery log (request, response truncated at 4 KB, result) is kept 30
  days, and **Redeliver** resends a payload.
- Order of arrival is not guaranteed; every payload has a `time`.
- Addresses on loopback, link-local and the cluster's own networks are always
  refused (and an administrator may restrict the LAN further): a blocked
  address is rejected when you save it.

The full payload of every event is in the
[webhook reference](https://github.com/fathorMB/GitStack/blob/main/docs/webhooks.md),
and the operations are in the [API reference](../api/index.md).
