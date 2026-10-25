import type { App, AppVersion } from '../../../lib/appsApi';
import type { Repository } from '../../../lib/reposApi';

export const repo = { id: 'r1', owner: { type: 'organization', name: 'acme' }, name: 'handbook', visibility: 'internal', archived: false, defaultBranch: 'main' } as Repository;

const base: App = {
  id: 'a1',
  name: 'web',
  owner: 'acme',
  repo: 'handbook',
  status: 'running',
  access: 'login',
  address: 'web-handbook-acme.apps.acme.it',
  image: { reference: 'homehub.local/acme/handbook/web:sha-7f3a91c', digest: 'sha256:7f3a91c' + '0'.repeat(57), source: 'registry', verified: true },
  activeVersion: 42,
  follow: { branch: 'main', paused: false },
  port: 8080,
  healthCheck: { path: '/healthz', intervalSeconds: 10 },
  copies: 2,
  readyCopies: 2,
  resources: { guaranteed: { cpuMillis: 500, memoryBytes: 512 * 1024 * 1024 }, limit: { cpuMillis: 1000, memoryBytes: 1024 * 1024 * 1024 } },
  usage: { cpuMillis: 620, memoryBytes: 410 * 1024 * 1024, requestsPerSecond: 38, errorRatio: 0.001, restarts24h: 0 },
  lastDeploy: { version: 42, state: 'serving', startedAt: '2026-10-25T10:00:00Z' },
  createdAt: '2026-10-01T10:00:00Z',
  updatedAt: '2026-10-25T10:00:00Z',
};

export function makeApp(over: Partial<App> = {}): App {
  return { ...base, ...over };
}

export const wiki = makeApp({
  id: 'a3',
  name: 'wiki',
  status: 'restarting',
  access: 'none',
  address: 'wiki-handbook-acme.apps.acme.it',
  image: { reference: 'docker.io/requarks/wiki:2.5', source: 'external', verified: false, runsAsRoot: true },
  follow: null,
  activeVersion: 3,
  copies: 1,
  readyCopies: 0,
});

export const worker = makeApp({ id: 'a2', name: 'search-worker', address: null, activeVersion: 19, copies: 1, readyCopies: 1 });

export function makeVersion(over: Partial<AppVersion> & { number: number }): AppVersion {
  return {
    trigger: 'branch_push',
    state: 'superseded',
    image: { reference: 'x', digest: 'sha256:c2d4e18' + '0'.repeat(57), source: 'registry', verified: true },
    imageAvailable: true,
    inBackup: false,
    branch: 'main',
    createdAt: '2026-10-24T10:00:00Z',
    createdBy: { id: 'u1', username: 'build-agent', type: 'agent' },
    ...over,
  };
}
