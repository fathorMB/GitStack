import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AppRoutes } from '../App';
import { CaGuidePage } from './CaGuidePage';
import { fetchInstanceCa } from '../lib/caApi';
import { fetchSession } from '../lib/authApi';

vi.mock('../lib/authApi', () => ({
  fetchSession: vi.fn(),
  fetchOidcProviders: vi.fn().mockResolvedValue([]),
  loginWithPassword: vi.fn(),
  oidcStartUrl: vi.fn(),
  signOut: vi.fn(),
}));

vi.mock('../lib/caApi', async () => {
  const actual = await vi.importActual<typeof import('../lib/caApi')>('../lib/caApi');
  return { ...actual, fetchInstanceCa: vi.fn() };
});

const FP = '4F:9A:21:C7:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:8E:03';
const INTERNAL = { mode: 'internal', fingerprintSha256: FP, downloadUrl: '/downloads/ca.crt' } as const;

function renderPage(path = '/help/ca') {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <CaGuidePage />
    </MemoryRouter>,
  );
}

const UA = {
  win: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/130',
  mac: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Safari/605',
  ios: 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Safari/604',
  and: 'Mozilla/5.0 (Linux; Android 14; Pixel 8) Chrome/130 Mobile',
};

function setUserAgent(ua: string) {
  vi.spyOn(window.navigator, 'userAgent', 'get').mockReturnValue(ua);
}

beforeEach(() => {
  vi.mocked(fetchInstanceCa).mockReset();
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('CaGuidePage', () => {
  it('mostra impronta, download e i passi del sistema riconosciuto dallo user agent', async () => {
    setUserAgent(UA.mac);
    vi.mocked(fetchInstanceCa).mockResolvedValue(INTERNAL);
    renderPage();
    expect(await screen.findByLabelText('SHA-256 fingerprint')).toHaveTextContent(`SHA-256 ${FP}`);
    expect(screen.getByText(/macOS · about 1 minute/)).toBeInTheDocument();
    expect(screen.getByText(/Always Trust/)).toBeInTheDocument();
    const dl = screen.getByRole('link', { name: /Download certificate/ });
    expect(dl).toHaveAttribute('href', '/downloads/ca.crt');
    expect(dl).toHaveAttribute('download', 'gitstack-ca.crt');
  }, 15000);

  it.each([
    ['win', /Trusted Root Certification Authorities/],
    ['ios', /Certificate Trust Settings/],
    ['and', /Install from device storage/],
  ] as const)('riconosce %s e permette di cambiare sistema', async (key, text) => {
    setUserAgent(UA[key]);
    vi.mocked(fetchInstanceCa).mockResolvedValue(INTERNAL);
    const user = userEvent.setup();
    renderPage();
    expect(await screen.findByText(text)).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Other device' }));
    const picker = screen.getByRole('group', { name: 'Device' });
    await user.click(within(picker).getByRole('button', { name: 'macOS' }));
    expect(screen.getByText(/Always Trust/)).toBeInTheDocument();
    expect(screen.queryByText(text)).not.toBeInTheDocument();
  }, 15000);

  it('senza ?app= non mostra «Prova»', async () => {
    setUserAgent(UA.win);
    vi.mocked(fetchInstanceCa).mockResolvedValue(INTERNAL);
    renderPage();
    await screen.findByLabelText('SHA-256 fingerprint');
    expect(screen.queryByRole('button', { name: /Try/ })).not.toBeInTheDocument();
  }, 15000);

  it('«Prova» con certificato fidato: la fetch riesce e la guida lo conferma', async () => {
    setUserAgent(UA.win);
    vi.mocked(fetchInstanceCa).mockResolvedValue(INTERNAL);
    const f = vi.fn().mockResolvedValue(new Response(null, { status: 200 }));
    vi.stubGlobal('fetch', f);
    const user = userEvent.setup();
    renderPage('/help/ca?app=sito.apps.gitstack.lan&app=backoffice.apps.gitstack.lan');
    await user.click(await screen.findByRole('button', { name: /Try: open sito/ }));
    expect(await screen.findByText(/this device trusts GitStack/)).toBeInTheDocument();
    expect(screen.getAllByText('secure')).toHaveLength(2);
    expect(f).toHaveBeenCalledWith('https://sito.apps.gitstack.lan/', expect.objectContaining({ mode: 'no-cors' }));
  }, 15000);

  it('«Prova» con certificato non fidato: la fetch fallisce e la guida dice non fidato', async () => {
    setUserAgent(UA.win);
    vi.mocked(fetchInstanceCa).mockResolvedValue(INTERNAL);
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')));
    const user = userEvent.setup();
    renderPage('/help/ca?app=sito.apps.gitstack.lan');
    await user.click(await screen.findByRole('button', { name: /Try: open sito/ }));
    expect(await screen.findByText(/Not trusted yet/)).toBeInTheDocument();
    expect(screen.getByText('not trusted')).toBeInTheDocument();
    expect(screen.queryByText(/this device trusts GitStack/)).not.toBeInTheDocument();
  }, 15000);

  it('con certificato del cliente o Let’s Encrypt dice che non serve installare niente', async () => {
    vi.mocked(fetchInstanceCa).mockResolvedValue({ mode: 'custom', fingerprintSha256: null, downloadUrl: null });
    renderPage();
    expect(await screen.findByText('Nothing to install')).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: /Download certificate/ })).not.toBeInTheDocument();
    expect(screen.queryByLabelText('SHA-256 fingerprint')).not.toBeInTheDocument();
  }, 15000);

  it('senza HTTPS (mode none) dice che non c’è niente da installare', async () => {
    vi.mocked(fetchInstanceCa).mockResolvedValue({ mode: 'none', fingerprintSha256: null, downloadUrl: null });
    renderPage();
    expect(await screen.findByText(/not served over HTTPS/)).toBeInTheDocument();
  }, 15000);

  it('errore del server: messaggio in linea', async () => {
    vi.mocked(fetchInstanceCa).mockRejectedValue(new Error('boom'));
    renderPage();
    expect(await screen.findByRole('alert')).toHaveTextContent(/not available/);
  }, 15000);
});

describe('rotta /help/ca', () => {
  it('si apre senza login: nessuna sessione, nessun redirect', async () => {
    vi.mocked(fetchSession).mockRejectedValue(new Error('401'));
    vi.mocked(fetchInstanceCa).mockResolvedValue(INTERNAL);
    render(
      <MemoryRouter initialEntries={['/help/ca']}>
        <AppRoutes />
      </MemoryRouter>,
    );
    expect(await screen.findByLabelText('SHA-256 fingerprint')).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole('button', { name: /Sign in/i })).not.toBeInTheDocument());
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument();
  }, 15000);
});
