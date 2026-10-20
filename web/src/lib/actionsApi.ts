// Actions ed esecuzioni (CI, mockup B1 e B2): avvolge le operazioni generate di @gitstack/api-client.
// Usa il client di default (nessun client esplicito) per l'interceptor 401.
import {
  cancelWorkflowRun as sdkCancelWorkflowRun,
  dispatchWorkflow as sdkDispatchWorkflow,
  getWorkflowJobLog as sdkGetWorkflowJobLog,
  getWorkflowRun as sdkGetWorkflowRun,
  listWorkflowArtifacts as sdkListWorkflowArtifacts,
  listWorkflowRuns as sdkListWorkflowRuns,
  listWorkflows as sdkListWorkflows,
  rerunWorkflowRun as sdkRerunWorkflowRun,
} from '@gitstack/api-client';
import type { Artifact, Workflow, WorkflowJob, WorkflowRun } from '@gitstack/api-client';
import { API_BASE_URL, unwrap, unwrapEmpty } from './http';

export type { Artifact, Workflow, WorkflowJob, WorkflowRun };

export async function fetchWorkflows(owner: string, repo: string): Promise<Workflow[]> {
  return unwrap(await sdkListWorkflows({ baseUrl: API_BASE_URL, path: { owner, repo } })).items;
}

export async function fetchRuns(owner: string, repo: string): Promise<WorkflowRun[]> {
  return unwrap(await sdkListWorkflowRuns({ baseUrl: API_BASE_URL, path: { owner, repo } })).items;
}

export async function fetchRun(owner: string, repo: string, runId: string): Promise<WorkflowRun> {
  return unwrap(await sdkGetWorkflowRun({ baseUrl: API_BASE_URL, path: { owner, repo, runId } }));
}

/** Avvio a mano (workflow_dispatch): serve write. */
export async function dispatchWorkflow(owner: string, repo: string, workflow: string, ref: string, inputs: Record<string, string>): Promise<void> {
  const body = Object.keys(inputs).length > 0 ? { ref, inputs } : { ref };
  unwrapEmpty(await sdkDispatchWorkflow({ baseUrl: API_BASE_URL, path: { owner, repo, workflow }, body }));
}

/** Annulla l'esecuzione. Chi chiama deve aver chiesto conferma (G8, come `gs run cancel`). */
export async function cancelRun(owner: string, repo: string, runId: string): Promise<WorkflowRun> {
  return unwrap(await sdkCancelWorkflowRun({ baseUrl: API_BASE_URL, path: { owner, repo, runId } }));
}

/** Riesegui: senza conferma, crea una nuova esecuzione. */
export async function rerunRun(owner: string, repo: string, runId: string): Promise<WorkflowRun> {
  return unwrap(await sdkRerunWorkflowRun({ baseUrl: API_BASE_URL, path: { owner, repo, runId } }));
}

/** Log del job (testo, segreti già mascherati dal server). Letto di nuovo a ogni giro finché il job è in corso. */
export async function fetchJobLog(owner: string, repo: string, jobId: string): Promise<string> {
  const res = await sdkGetWorkflowJobLog({ baseUrl: API_BASE_URL, path: { owner, repo, jobId }, parseAs: 'text' });
  const text = (res as { data?: unknown }).data;
  if (typeof text === 'string') return text;
  return unwrap(res as { data?: string; error?: unknown; response?: Response });
}

export async function fetchArtifacts(owner: string, repo: string, runId: string): Promise<Artifact[]> {
  return unwrap(await sdkListWorkflowArtifacts({ baseUrl: API_BASE_URL, path: { owner, repo, runId } })).items;
}

/** Indirizzo di scaricamento di un artefatto (sessione o token, come gli archivi). */
export function artifactUrl(owner: string, repo: string, artifactId: string): string {
  const e = encodeURIComponent;
  return `${API_BASE_URL}/repos/${e(owner)}/${e(repo)}/actions/artifacts/${e(artifactId)}/download`;
}
