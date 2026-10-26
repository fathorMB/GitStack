// CI v2 (regole CI4, CI5, CI7 in [c_baa86c8ddb5db70d]): segreti e variabili di repo
// e di organizzazione, runner esterni, coda. Solo le operazioni del contratto
// OpenAPI (api/openapi.yaml): il valore dei segreti entra nel corpo della PUT e
// non torna mai nella risposta, per questo la UI non puo' rileggerlo.
// Usa il client di default (nessun client esplicito) per l'interceptor 401.
import {
  createCiRunnerRegistrationToken as sdkCreateCiRunnerRegistrationToken,
  deleteCiRunner as sdkDeleteCiRunner,
  deleteRepositoryActionsSecret as sdkDeleteRepositoryActionsSecret,
  deleteRepositoryActionsVariable as sdkDeleteRepositoryActionsVariable,
  getRepositoryActionsPermissions as sdkGetRepositoryActionsPermissions,
  getRepositoryActionsPullMirror as sdkGetRepositoryActionsPullMirror,
  putRepositoryActionsPullMirror as sdkPutRepositoryActionsPullMirror,
  getCiQueue as sdkGetCiQueue,
  listCiRunners as sdkListCiRunners,
  listOrganizationActionsSecrets as sdkListOrganizationActionsSecrets,
  listRepositoryActionsSecrets as sdkListRepositoryActionsSecrets,
  listRepositoryActionsVariables as sdkListRepositoryActionsVariables,
  putRepositoryActionsPermissions as sdkPutRepositoryActionsPermissions,
  putRepositoryActionsSecret as sdkPutRepositoryActionsSecret,
  putRepositoryActionsVariable as sdkPutRepositoryActionsVariable,
} from '@gitstack/api-client';
import type {
  ActionsJobTokenDefaults,
  ActionsPullMirrorSettings,
  ActionsSecret,
  ActionsSecretInputWritable,
  ActionsSecretList,
  ActionsVariable,
  ActionsVariableInput,
  ActionsVariableList,
  CiQueue,
  CiRunner,
  CiRunnerList,
  CiRunnerRegistrationToken,
} from '@gitstack/api-client';
import { API_BASE_URL, unwrap, unwrapEmpty } from './http';

export type {
  ActionsJobTokenDefaults,
  ActionsPullMirrorSettings,
  ActionsSecret,
  ActionsSecretList,
  ActionsVariable,
  ActionsVariableList,
  CiQueue,
  CiRunner,
  CiRunnerList,
  CiRunnerRegistrationToken,
};

/* ===== Segreti e variabili del repo (solo admin) ===== */

export async function fetchRepoSecrets(owner: string, repo: string): Promise<ActionsSecretList> {
  return unwrap(
    await sdkListRepositoryActionsSecrets({
      baseUrl: API_BASE_URL,
      path: { owner, repo },
    }),
  );
}

/** Crea o sostituisce un segreto: la risposta ha solo i metadati, mai il valore. */
export async function putRepoSecret(
  owner: string,
  repo: string,
  input: ActionsSecretInputWritable,
): Promise<ActionsSecret> {
  return unwrap(
    await sdkPutRepositoryActionsSecret({
      baseUrl: API_BASE_URL,
      path: { owner, repo },
      body: input,
    }),
  );
}

export async function fetchRepoVariables(owner: string, repo: string): Promise<ActionsVariableList> {
  return unwrap(
    await sdkListRepositoryActionsVariables({
      baseUrl: API_BASE_URL,
      path: { owner, repo },
    }),
  );
}

export async function putRepoVariable(
  owner: string,
  repo: string,
  input: ActionsVariableInput,
): Promise<ActionsVariable> {
  return unwrap(
    await sdkPutRepositoryActionsVariable({
      baseUrl: API_BASE_URL,
      path: { owner, repo },
      body: input,
    }),
  );
}

export async function deleteRepoSecret(owner: string, repo: string, name: string): Promise<void> {
  unwrapEmpty(await sdkDeleteRepositoryActionsSecret({ baseUrl: API_BASE_URL, path: { owner, repo, name } }));
}

export async function deleteRepoVariable(owner: string, repo: string, name: string): Promise<void> {
  unwrapEmpty(await sdkDeleteRepositoryActionsVariable({ baseUrl: API_BASE_URL, path: { owner, repo, name } }));
}

/* ===== Permessi di default (tetto) del token del job del repo (solo admin) ===== */

export async function fetchRepoTokenPermissions(owner: string, repo: string): Promise<ActionsJobTokenDefaults> {
  return unwrap(await sdkGetRepositoryActionsPermissions({ baseUrl: API_BASE_URL, path: { owner, repo } }));
}

export async function putRepoTokenPermissions(
  owner: string,
  repo: string,
  input: ActionsJobTokenDefaults,
): Promise<ActionsJobTokenDefaults> {
  return unwrap(await sdkPutRepositoryActionsPermissions({ baseUrl: API_BASE_URL, path: { owner, repo }, body: input }));
}

/* ===== CI sul mirror in pull (CI3, solo admin) ===== */

export async function fetchRepoPullMirrorCI(owner: string, repo: string): Promise<ActionsPullMirrorSettings> {
  return unwrap(await sdkGetRepositoryActionsPullMirror({ baseUrl: API_BASE_URL, path: { owner, repo } }));
}

export async function putRepoPullMirrorCI(owner: string, repo: string, runOnPullMirror: boolean): Promise<ActionsPullMirrorSettings> {
  return unwrap(await sdkPutRepositoryActionsPullMirror({ baseUrl: API_BASE_URL, path: { owner, repo }, body: { runOnPullMirror } }));
}

/* ===== Segreti ereditati dall'organizzazione ===== */

export async function fetchOrgSecrets(org: string): Promise<ActionsSecretList> {
  return unwrap(
    await sdkListOrganizationActionsSecrets({
      baseUrl: API_BASE_URL,
      path: { org },
    }),
  );
}

/* ===== Admin · CI e runner (solo amministratore d'istanza) ===== */

export async function fetchCiRunners(): Promise<CiRunnerList> {
  return unwrap(await sdkListCiRunners({ baseUrl: API_BASE_URL }));
}

/** Token di registrazione: il valore è nella sola risposta 201, nessuna GET lo rilegge. */
export async function createCiRegistrationToken(): Promise<CiRunnerRegistrationToken> {
  return unwrap(await sdkCreateCiRunnerRegistrationToken({ baseUrl: API_BASE_URL }));
}

export async function deleteCiRunner(runnerId: string): Promise<void> {
  unwrapEmpty(
    await sdkDeleteCiRunner({
      baseUrl: API_BASE_URL,
      path: { runnerId },
    }),
  );
}

export async function fetchCiQueue(): Promise<CiQueue> {
  return unwrap(await sdkGetCiQueue({ baseUrl: API_BASE_URL }));
}
