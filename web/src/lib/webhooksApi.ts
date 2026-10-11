// Webhook API (M-06/C6, C7): operazioni su webhook di repo e organizzazione.
// Usa il client di default (nessun client esplicito) per l'interceptor 401.
import {
  createOrgWebhook as sdkCreateOrgWebhook,
  createRepoWebhook as sdkCreateRepoWebhook,
  deleteOrgWebhook as sdkDeleteOrgWebhook,
  deleteRepoWebhook as sdkDeleteRepoWebhook,
  getOrgWebhook as sdkGetOrgWebhook,
  getOrgWebhookDelivery as sdkGetOrgWebhookDelivery,
  getRepoWebhook as sdkGetRepoWebhook,
  getRepoWebhookDelivery as sdkGetRepoWebhookDelivery,
  listOrgWebhookDeliveries as sdkListOrgWebhookDeliveries,
  listOrgWebhooks as sdkListOrgWebhooks,
  listRepoWebhookDeliveries as sdkListRepoWebhookDeliveries,
  listRepoWebhooks as sdkListRepoWebhooks,
  reactivateOrgWebhook as sdkReactivateOrgWebhook,
  reactivateRepoWebhook as sdkReactivateRepoWebhook,
  redeliverOrgWebhookDelivery as sdkRedeliverOrgWebhookDelivery,
  redeliverRepoWebhookDelivery as sdkRedeliverRepoWebhookDelivery,
  updateOrgWebhook as sdkUpdateOrgWebhook,
  updateRepoWebhook as sdkUpdateRepoWebhook,
} from '@gitstack/api-client';
import type {
  Webhook,
  WebhookDelivery,
  WebhookDeliveryDetail,
  WebhookDeliveryList,
  WebhookDeliverySummary,
  WebhookEvent,
  WebhookList,
} from '@gitstack/api-client';
import { API_BASE_URL, unwrap, unwrapEmpty } from './http';

export type {
  Webhook,
  WebhookList,
  WebhookDelivery,
  WebhookDeliverySummary,
  WebhookDeliveryDetail,
  WebhookDeliveryList,
  WebhookEvent,
};

/* ===== Repo webhooks (admin del repo) ===== */

export async function fetchRepoWebhooks(owner: string, repo: string): Promise<WebhookList> {
  return unwrap(
    await sdkListRepoWebhooks({
      baseUrl: API_BASE_URL,
      path: { owner, repo },
      query: { page: 1, perPage: 50 },
    }),
  );
}

export async function fetchRepoWebhook(owner: string, repo: string, hookId: string): Promise<Webhook> {
  return unwrap(
    await sdkGetRepoWebhook({
      baseUrl: API_BASE_URL,
      path: { owner, repo, hookId },
    }),
  );
}

export async function createRepoWebhook(
  owner: string,
  repo: string,
  input: { url: string; events: WebhookEvent[]; secret?: string },
): Promise<Webhook> {
  return unwrap(
    await sdkCreateRepoWebhook({
      baseUrl: API_BASE_URL,
      path: { owner, repo },
      body: input,
    }),
  );
}

export async function updateRepoWebhook(
  owner: string,
  repo: string,
  hookId: string,
  input: { url?: string; events?: WebhookEvent[]; active?: boolean; secret?: string },
): Promise<Webhook> {
  return unwrap(
    await sdkUpdateRepoWebhook({
      baseUrl: API_BASE_URL,
      path: { owner, repo, hookId },
      body: input,
    }),
  );
}

export async function deleteRepoWebhook(owner: string, repo: string, hookId: string): Promise<void> {
  unwrapEmpty(
    await sdkDeleteRepoWebhook({
      baseUrl: API_BASE_URL,
      path: { owner, repo, hookId },
    }),
  );
}

export async function reactivateRepoWebhook(owner: string, repo: string, hookId: string): Promise<Webhook> {
  return unwrap(
    await sdkReactivateRepoWebhook({
      baseUrl: API_BASE_URL,
      path: { owner, repo, hookId },
    }),
  );
}

export async function fetchRepoWebhookDeliveries(
  owner: string,
  repo: string,
  hookId: string,
): Promise<WebhookDeliveryList> {
  return unwrap(
    await sdkListRepoWebhookDeliveries({
      baseUrl: API_BASE_URL,
      path: { owner, repo, hookId },
      query: { page: 1, perPage: 20 },
    }),
  );
}

export async function fetchRepoWebhookDeliveryDetail(
  owner: string,
  repo: string,
  hookId: string,
  deliveryId: string,
): Promise<WebhookDeliveryDetail> {
  return unwrap(
    await sdkGetRepoWebhookDelivery({
      baseUrl: API_BASE_URL,
      path: { owner, repo, hookId, deliveryId },
    }),
  );
}

export async function redeliverRepoWebhookDelivery(
  owner: string,
  repo: string,
  hookId: string,
  deliveryId: string,
): Promise<void> {
  unwrapEmpty(
    await sdkRedeliverRepoWebhookDelivery({
      baseUrl: API_BASE_URL,
      path: { owner, repo, hookId, deliveryId },
    }),
  );
}

/* ===== Org webhooks (owner dell'organizzazione) ===== */

export async function fetchOrgWebhooks(org: string): Promise<WebhookList> {
  return unwrap(
    await sdkListOrgWebhooks({
      baseUrl: API_BASE_URL,
      path: { org },
      query: { page: 1, perPage: 50 },
    }),
  );
}

export async function fetchOrgWebhook(org: string, hookId: string): Promise<Webhook> {
  return unwrap(
    await sdkGetOrgWebhook({
      baseUrl: API_BASE_URL,
      path: { org, hookId },
    }),
  );
}

export async function createOrgWebhook(
  org: string,
  input: { url: string; events: WebhookEvent[]; secret?: string },
): Promise<Webhook> {
  return unwrap(
    await sdkCreateOrgWebhook({
      baseUrl: API_BASE_URL,
      path: { org },
      body: input,
    }),
  );
}

export async function updateOrgWebhook(
  org: string,
  hookId: string,
  input: { url?: string; events?: WebhookEvent[]; active?: boolean; secret?: string },
): Promise<Webhook> {
  return unwrap(
    await sdkUpdateOrgWebhook({
      baseUrl: API_BASE_URL,
      path: { org, hookId },
      body: input,
    }),
  );
}

export async function deleteOrgWebhook(org: string, hookId: string): Promise<void> {
  unwrapEmpty(
    await sdkDeleteOrgWebhook({
      baseUrl: API_BASE_URL,
      path: { org, hookId },
    }),
  );
}

export async function reactivateOrgWebhook(org: string, hookId: string): Promise<Webhook> {
  return unwrap(
    await sdkReactivateOrgWebhook({
      baseUrl: API_BASE_URL,
      path: { org, hookId },
    }),
  );
}

export async function fetchOrgWebhookDeliveries(
  org: string,
  hookId: string,
): Promise<WebhookDeliveryList> {
  return unwrap(
    await sdkListOrgWebhookDeliveries({
      baseUrl: API_BASE_URL,
      path: { org, hookId },
      query: { page: 1, perPage: 20 },
    }),
  );
}

export async function fetchOrgWebhookDeliveryDetail(
  org: string,
  hookId: string,
  deliveryId: string,
): Promise<WebhookDeliveryDetail> {
  return unwrap(
    await sdkGetOrgWebhookDelivery({
      baseUrl: API_BASE_URL,
      path: { org, hookId, deliveryId },
    }),
  );
}

export async function redeliverOrgWebhookDelivery(
  org: string,
  hookId: string,
  deliveryId: string,
): Promise<void> {
  unwrapEmpty(
    await sdkRedeliverOrgWebhookDelivery({
      baseUrl: API_BASE_URL,
      path: { org, hookId, deliveryId },
    }),
  );
}
