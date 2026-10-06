import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { DownloadsPage } from './DownloadsPage';

const SHA = 'a'.repeat(64);

function index(over: Record<string, unknown> = {}) {
  return {
    version: 'sha-1234abc',
    binaries: [
      { os: 'linux', arch: 'amd64', file: 'gs_linux_amd64', url: '/downloads/gs_linux_amd64', sha256: SHA, size: 5242880 },
      { os: 'linux', arch: 'arm64', file: 'gs_linux_arm64', url: '/downloads/gs_linux_arm64', sha256: SHA, size: 5000000 },
      { os: 'darwin', arch: 'arm64', file: 'gs_darwin_arm64', url: '/downloads/gs_darwin_arm64', sha256: SHA, size: 5000000 },
      { os: 'windows', arch: 'amd64', file: 'gs_windows_amd64.exe', url: '/downloads/gs_windows_amd64.exe', sha256: SHA, size: 5000000 },
    ],
    checksums: '/downloads/SHA256SUMS',
    skills: { file: 'gs-skills.zip', url: '/downloads/gs-skills.zip', sha256: SHA },
    install: { sh: '/install-gs.sh', ps1: '/install-gs.ps1' },
    ca_cert: null,
    ...over,
  };
}

function mockFetch(res: Response | Promise<Response>) {
  const f = vi.fn().mockReturnValue(res);
  vi.stubGlobal('fetch', f);
  return f;
}

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/downloads']}>
      <DownloadsPage />
    </MemoryRouter>,
  );
}

describe('DownloadsPage', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('legge /downloads/index.json e mostra binari con checksum, skills e SHA256SUMS', async () => {
    const f = mockFetch(Promise.resolve(new Response(JSON.stringify(index()), { status: 200 })));
    renderPage();
    const list = await screen.findByRole('list', { name: 'Binaries' });
    expect(f.mock.calls[0]?.[0]).toBe('/downloads/index.json');
    expect(screen.getByText('GitStack sha-1234abc')).toBeInTheDocument();
    expect(within(list).getByText('Linux')).toBeInTheDocument();
    expect(within(list).getByText('macOS')).toBeInTheDocument();
    expect(within(list).getByText('Windows')).toBeInTheDocument();
    expect(within(list).getByRole('link', { name: /amd64 gs_linux_amd64/ })).toHaveAttribute('href', '/downloads/gs_linux_amd64');
    expect(within(list).getByRole('link', { name: /Apple silicon/ })).toHaveAttribute('href', '/downloads/gs_darwin_arm64');
    expect(within(list).getAllByText(/sha256 aaaaaaaaaaaa/)).toHaveLength(4);
    expect(screen.getByRole('link', { name: 'SHA256SUMS' })).toHaveAttribute('href', '/downloads/SHA256SUMS');
    expect(screen.getByRole('link', { name: 'Download skills (.zip)' })).toHaveAttribute('href', '/downloads/gs-skills.zip');
    expect(screen.queryByText(/MCP/)).not.toBeInTheDocument();
  }, 15000);

  it('comandi copiabili con l\'host corrente, sh e PowerShell', async () => {
    mockFetch(Promise.resolve(new Response(JSON.stringify(index()), { status: 200 })));
    const user = userEvent.setup();
    renderPage();
    await screen.findByRole('list', { name: 'Binaries' });
    const origin = window.location.origin;
    expect(screen.getByLabelText('Install command')).toHaveTextContent(`curl -fsSL ${origin}/install-gs.sh | sh`);
    await user.click(screen.getByRole('button', { name: 'Windows' }));
    expect(screen.getByLabelText('Install command')).toHaveTextContent(`irm ${origin}/install-gs.ps1 | iex`);
    expect(screen.getByText(new RegExp(`gs auth login --host ${window.location.host} --web`))).toBeInTheDocument();
  }, 15000);

  it('il pulsante Copy mette il comando negli appunti', async () => {
    mockFetch(Promise.resolve(new Response(JSON.stringify(index()), { status: 200 })));
    const user = userEvent.setup();
    const writeText = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue();
    renderPage();
    await screen.findByRole('list', { name: 'Binaries' });
    await user.click(screen.getAllByRole('button', { name: 'Copy' })[0]!);
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(`curl -fsSL ${window.location.origin}/install-gs.sh | sh`));
  }, 15000);

  it('collegamento al certificato della CA solo quando c\'e\'', async () => {
    mockFetch(Promise.resolve(new Response(JSON.stringify(index({ ca_cert: '/downloads/ca.crt' })), { status: 200 })));
    const { unmount } = renderPage();
    expect(await screen.findByRole('link', { name: /CA certificate/ })).toHaveAttribute('href', '/downloads/ca.crt');
    unmount();
    mockFetch(Promise.resolve(new Response(JSON.stringify(index()), { status: 200 })));
    renderPage();
    await screen.findByRole('list', { name: 'Binaries' });
    expect(screen.queryByRole('link', { name: /CA certificate/ })).not.toBeInTheDocument();
  }, 15000);

  it('indice assente: messaggio, la pagina resta e i comandi si vedono', async () => {
    mockFetch(Promise.resolve(new Response('not found', { status: 404 })));
    renderPage();
    expect(await screen.findByRole('alert')).toHaveTextContent(/downloads index is not available \(HTTP 404\)/);
    expect(screen.getByRole('heading', { name: 'CLI & skills' })).toBeInTheDocument();
    expect(screen.getByLabelText('Install command')).toHaveTextContent(`${window.location.origin}/install-gs.sh`);
    expect(screen.queryByRole('list', { name: 'Binaries' })).not.toBeInTheDocument();
  }, 15000);

  it('errore di rete: messaggio, niente crash', async () => {
    mockFetch(Promise.reject(new TypeError('network')));
    renderPage();
    expect(await screen.findByRole('alert')).toHaveTextContent(/Could not reach the server/);
  }, 15000);
});
