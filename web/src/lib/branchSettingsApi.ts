// Impostazioni del branch (v1.1, mockup 28): via client generato (client di default: l'interceptor 401 le vede).
import { getBranchSettings, updateBranchSettings } from '@gitstack/api-client';
import type { BranchSettings, UpdateBranchSettingsInput } from '@gitstack/api-client';
import { API_BASE_URL, unwrap } from './http';

export type { BranchSettings, UpdateBranchSettingsInput };

export async function fetchBranchSettings(owner: string, repo: string): Promise<BranchSettings> {
  return unwrap(await getBranchSettings({ baseUrl: API_BASE_URL, path: { owner, repo } }));
}

export async function saveBranchSettings(owner: string, repo: string, input: UpdateBranchSettingsInput): Promise<BranchSettings> {
  return unwrap(await updateBranchSettings({ baseUrl: API_BASE_URL, path: { owner, repo }, body: input }));
}
