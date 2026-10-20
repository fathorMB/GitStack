import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Release } from '../../lib/releasesApi';
import type { Repository } from '../../lib/reposApi';
import { ReleasesPage } from './ReleasesPage';

vi.mock('../../lib/releasesApi', async (orig) => ({
  ...(await orig<typeof import('../../lib/releasesApi')>()),
  fetchReleases: vi.fn(),
  createRelease: vi.fn(),
  updateRelease: vi.fn(),
  uploadReleaseAsset: vi.fn(),
}));
vi.mock('../../lib/useCanWrite', () => ({ useCanWrite: vi.fn() }));

import { createRelease, fetchReleases, updateRelease, uploadReleaseAsset } from '../../lib/releasesApi';
import { useCanWrite } from '../../lib/useCanWrite';

const repo = { id: 'r1', owner: { type: 'organization', name: 'acme' }, name: 'api', archived: false } as Repository;

const rel = (over: Partial<Release>): Release => ({
  id: 'x',
  tagName: 'v1',
  name: 'One',
  body: '',
  draft: false,
  prerelease: false,
  latest: false,
  assets: [],
  createdAt: '2026-10-01T10:00:00Z',
  publishedAt: '2026-10-01T10:00:00Z',
  ...over,
});

const sha = '1f2e' + '0'.repeat(58) + '9a';
const list = [
  rel({
    id: 'a',
    tagName: 'v1.0.0-rc.3',
    name: 'GitStack 1.0.0-rc.3',
    prerelease: true,
    body: '### What is changed\n\n- Pull mirror pause',
    assets: [{ id: 'as1', name: 'gs-linux-amd64.tar.gz', sizeBytes: 10276044, sha256: sha }],
  }),
  rel({ id: 'b', tagName: 'v0.9.0', name: 'GitStack 0.9.0', latest: true }),
  rel({ id: 'c', tagName: 'v1.0.0', name: 'GitStack 1.0.0', draft: true }),
];

function setup(write: boolean) {
  vi.mocked(useCanWrite).mockReturnValue(write);
  vi.mocked(fetchReleases).mockResolvedValue({ items: list });
  render(
    <MemoryRouter>
      <ReleasesPage repo={repo} />
    </MemoryRouter>,
  );
}

// Timeout ampio: sotto carico i test con userEvent superano i 5 s di default.
describe('pagina Releases (mockup B7)', { timeout: 30000 }, () => {
  beforeEach(() => {
    vi.mocked(fetchReleases).mockReset();
    vi.mocked(createRelease).mockReset();
    vi.mocked(updateRelease).mockReset();
    vi.mocked(uploadReleaseAsset).mockReset();
  });

  it('mostra Latest, pre-release, note e file con SHA-256; la bozza solo a chi ha write', async () => {
    setup(true);
    const items = await screen.findByRole('list', { name: 'Releases' });
    expect(await within(items).findByText('Pre-release')).toBeInTheDocument();
    expect(within(items).getByText('Latest')).toBeInTheDocument();
    expect(within(items).getByText(/Draft · visible to writers only/)).toBeInTheDocument();
    expect(within(items).getByText('Pull mirror pause')).toBeInTheDocument();
    const asset = within(items).getByRole('link', { name: 'gs-linux-amd64.tar.gz' });
    expect(asset).toHaveAttribute('href', expect.stringContaining('/repos/acme/api/releases/a/assets/as1/download'));
    expect(within(items).getByText('sha256:1f2e…9a')).toBeInTheDocument();
    expect(within(items).getByRole('link', { name: 'Download v1.0.0-rc.3 as ZIP' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Tags' })).toHaveAttribute('href', '/acme/api/tags');
  });

  it('senza write non mostra bozze, né Draft a new release, né Edit', async () => {
    setup(false);
    await screen.findByText('GitStack 0.9.0');
    expect(screen.queryByText('GitStack 1.0.0')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Draft a new release/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /^Edit/ })).not.toBeInTheDocument();
  });

  it('crea una release e carica i file allegati', async () => {
    setup(true);
    vi.mocked(createRelease).mockResolvedValue(rel({ id: 'n', tagName: 'v2.0.0' }));
    vi.mocked(uploadReleaseAsset).mockResolvedValue({ id: 'f', name: 'a.txt', sizeBytes: 1, sha256: 'ab' });
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: /Draft a new release/ }));
    await user.type(screen.getByLabelText('Tag'), 'v2.0.0');
    await user.type(screen.getByLabelText('Release title'), 'Two');
    await user.click(screen.getByRole('checkbox', { name: 'This is a pre-release' }));
    await user.click(screen.getByRole('checkbox', { name: 'Generate release notes' }));
    const file = new File(['x'], 'a.txt', { type: 'text/plain' });
    await user.upload(screen.getByLabelText('Attach files'), file);
    await user.click(screen.getByRole('button', { name: 'Publish release' }));
    await waitFor(() => expect(createRelease).toHaveBeenCalled());
    expect(vi.mocked(createRelease).mock.calls[0]?.[2]).toMatchObject({
      tagName: 'v2.0.0',
      name: 'Two',
      prerelease: true,
      draft: false,
      generateNotes: true,
    });
    await waitFor(() => expect(uploadReleaseAsset).toHaveBeenCalledWith('acme', 'api', 'n', file));
    await waitFor(() => expect(fetchReleases).toHaveBeenCalledTimes(2));
  });

  it('modifica una release: pubblica la bozza e allega un file', async () => {
    setup(true);
    vi.mocked(updateRelease).mockResolvedValue(rel({ id: 'c', draft: false }));
    vi.mocked(uploadReleaseAsset).mockResolvedValue({ id: 'f', name: 'b.txt', sizeBytes: 1, sha256: 'ab' });
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'Edit GitStack 1.0.0' }));
    expect(screen.getByLabelText('Tag')).toHaveAttribute('readonly');
    await user.click(screen.getByRole('checkbox', { name: 'Save as draft' }));
    const file = new File(['y'], 'b.txt');
    await user.upload(screen.getByLabelText('Attach files'), file);
    await user.click(screen.getByRole('button', { name: 'Save release' }));
    await waitFor(() => expect(updateRelease).toHaveBeenCalledWith('acme', 'api', 'c', expect.objectContaining({ name: 'GitStack 1.0.0', draft: false })));
    await waitFor(() => expect(uploadReleaseAsset).toHaveBeenCalledWith('acme', 'api', 'c', file));
  });

  it('passando da Edit di A a Edit di B senza Cancel il form riparte da B: campi, file e payload', async () => {
    setup(true);
    vi.mocked(updateRelease).mockResolvedValue(rel({ id: 'b' }));
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'Edit GitStack 1.0.0-rc.3' }));
    await user.type(screen.getByLabelText('Release title'), ' changed');
    await user.upload(screen.getByLabelText('Attach files'), new File(['x'], 'a.txt'));
    expect(screen.getByLabelText('Tag')).toHaveValue('v1.0.0-rc.3');
    expect(screen.getByText(/a\.txt/)).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Edit GitStack 0.9.0' }));
    expect(screen.getByLabelText('Tag')).toHaveValue('v0.9.0');
    expect(screen.getByLabelText('Release title')).toHaveValue('GitStack 0.9.0');
    expect(screen.getByLabelText('Notes (Markdown)')).toHaveValue('');
    expect(screen.getByRole('checkbox', { name: 'This is a pre-release' })).not.toBeChecked();
    expect(screen.queryByLabelText('Files to upload')).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Save release' }));
    await waitFor(() => expect(updateRelease).toHaveBeenCalledTimes(1));
    expect(updateRelease).toHaveBeenCalledWith('acme', 'api', 'b', { name: 'GitStack 0.9.0', body: '', draft: false, prerelease: false });
    expect(uploadReleaseAsset).not.toHaveBeenCalled();
  });

  it('da nuova release a Edit di una esistente azzera i campi digitati', async () => {
    setup(true);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: /Draft a new release/ }));
    await user.type(screen.getByLabelText('Tag'), 'v7');
    await user.upload(screen.getByLabelText('Attach files'), new File(['x'], 'n.txt'));
    await user.click(screen.getByRole('button', { name: 'Edit GitStack 0.9.0' }));
    expect(screen.getByLabelText('Tag')).toHaveValue('v0.9.0');
    expect(screen.getByLabelText('Tag')).toHaveAttribute('readonly');
    expect(screen.queryByLabelText('Files to upload')).not.toBeInTheDocument();
    expect(screen.queryByRole('checkbox', { name: 'Generate release notes' })).not.toBeInTheDocument();
  });

  it('upload fallito: il retry non ricrea la release né ricarica il file già riuscito', async () => {
    setup(true);
    vi.mocked(createRelease).mockResolvedValue(rel({ id: 'n', tagName: 'v3' }));
    vi.mocked(uploadReleaseAsset)
      .mockResolvedValueOnce({ id: 'f1', name: 'one.txt', sizeBytes: 1, sha256: 'ab' })
      .mockRejectedValueOnce(new Error('boom'))
      .mockResolvedValueOnce({ id: 'f2', name: 'two.txt', sizeBytes: 1, sha256: 'cd' });
    const one = new File(['1'], 'one.txt');
    const two = new File(['2'], 'two.txt');
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: /Draft a new release/ }));
    await user.type(screen.getByLabelText('Tag'), 'v3');
    await user.upload(screen.getByLabelText('Attach files'), [one, two]);
    await user.click(screen.getByRole('button', { name: 'Publish release' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(/two\.txt was not uploaded/);
    expect(createRelease).toHaveBeenCalledTimes(1);
    expect(uploadReleaseAsset).toHaveBeenCalledTimes(2);
    await user.click(screen.getByRole('button', { name: 'Retry upload' }));
    await waitFor(() => expect(uploadReleaseAsset).toHaveBeenCalledTimes(3));
    expect(createRelease).toHaveBeenCalledTimes(1);
    expect(vi.mocked(uploadReleaseAsset).mock.calls.map((c) => [c[2], c[3]])).toEqual([
      ['n', one],
      ['n', two],
      ['n', two],
    ]);
    await waitFor(() => expect(fetchReleases).toHaveBeenCalledTimes(2));
  });

  it('mostra l’errore del server in linea', async () => {
    setup(true);
    vi.mocked(createRelease).mockRejectedValue(new Error('boom'));
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: /Draft a new release/ }));
    await user.type(screen.getByLabelText('Tag'), 'v9');
    await user.click(screen.getByRole('button', { name: 'Publish release' }));
    expect(await screen.findByRole('alert')).toBeInTheDocument();
    expect(uploadReleaseAsset).not.toHaveBeenCalled();
  });
});
