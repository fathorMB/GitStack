import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, expect, it, vi } from 'vitest';
import * as api from '../lib/databasesApi';
import { fetchSession } from '../lib/authApi';
import { fetchOrganizations } from '../lib/orgsApi';
import { ApiError } from '../lib/http';
import { DatabaseDetailPage, DatabasesPage, NewDatabasePage } from './DatabasesPage';
import { AppDatabases } from './repos/apps/AppDatabases';

vi.mock('../lib/databasesApi');
vi.mock('../lib/authApi');
vi.mock('../lib/orgsApi');

const db = { id: 'abc', owner: 'alice', name: 'db', pgMajor: 16 as const, status: 'running' as const, sizeBytes: 1073741824, cpuMillis: 100, memoryBytes: 1, lanEnabled: false, linkCount: 0, createdAt: 'today', updatedAt: 'today' };
const credentials = { username: 'db_owner', password: 'secret-value', database: 'db', sslMode: 'verify-full' as const };

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(fetchSession).mockResolvedValue({ user: { username: 'alice' } } as Awaited<ReturnType<typeof fetchSession>>);
  vi.mocked(fetchOrganizations).mockResolvedValue([]);
  vi.mocked(api.fetchDatabases).mockResolvedValue({ items: [db] });
  vi.mocked(api.fetchDatabase).mockResolvedValue(db);
  vi.mocked(api.fetchDatabaseLinks).mockResolvedValue({ items: [] });
  vi.mocked(api.fetchDatabaseBackups).mockResolvedValue({ items: [{ id: 'b1', kind: 'on_demand', status: 'completed', createdAt: 'today' }] });
  vi.mocked(api.fetchDatabaseLan).mockResolvedValue({ enabled: false, host: null, port: null, caCertificate: null, ufwCommand: null });
});

it('crea e mostra la password solo dopo azione esplicita', async () => {
  vi.mocked(api.addDatabase).mockResolvedValue({ database: db, credentials });
  render(<MemoryRouter><NewDatabasePage /></MemoryRouter>);
  await screen.findByRole('option', { name: 'alice' });
  await userEvent.type(screen.getByLabelText('Name'), 'db');
  await userEvent.click(screen.getByRole('button', { name: 'Create database' }));
  await screen.findByText('Database created');
  expect(screen.queryByText('secret-value')).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole('button', { name: 'Show password' }));
  expect(screen.getByText('secret-value')).toBeInTheDocument();
}, 20000);

it('spiega la funzione spenta senza errore', async () => {
  vi.mocked(api.fetchDatabases).mockRejectedValue(new ApiError({ error: { code: 'databases_disabled', message: 'disabled' } }, 422));
  render(<MemoryRouter><DatabasesPage /></MemoryRouter>);
  expect(await screen.findByText(/Ask an administrator to enable/)).toBeInTheDocument();
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('mostra la capacità mancante nel form', async () => {
  vi.mocked(api.addDatabase).mockRejectedValue(new ApiError({ error: { code: 'insufficient_capacity', message: 'Insufficient capacity', details: { missing: { cpuMillis: 100, memoryBytes: 200, diskBytes: 300 } } } }, 409));
  render(<MemoryRouter><NewDatabasePage /></MemoryRouter>);
  await screen.findByRole('option', { name: 'alice' });
  await userEvent.type(screen.getByLabelText('Name'), 'db');
  await userEvent.click(screen.getByRole('button', { name: 'Create database' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('cpuMillis: 100, memoryBytes: 200, diskBytes: 300');
}, 20000);

it('mantiene la pagina visibile se la chiusura LAN fallisce', async () => {
  vi.mocked(api.fetchDatabaseLan).mockResolvedValue({ enabled: true, host: 'db.example', port: 15432, caCertificate: '', ufwCommand: null });
  vi.mocked(api.changeDatabaseLan).mockRejectedValue(new ApiError({ error: { code: 'lan_close_failed', message: 'LAN close failed' } }, 503));
  render(<MemoryRouter initialEntries={['/databases/alice/db']}><Routes><Route path="/databases/:owner/:name" element={<DatabaseDetailPage />} /></Routes></MemoryRouter>);
  await userEvent.click(await screen.findByRole('button', { name: 'Disable LAN access' }));
  expect(await screen.findByText('LAN close failed')).toBeInTheDocument();
  expect(screen.getByText(/Status: running/)).toBeInTheDocument();
}, 20000);

it('collega, esegue backup, ripristina, cambia LAN ed elimina con conferma', async () => {
  vi.mocked(api.addDatabaseLink).mockResolvedValue({} as Awaited<ReturnType<typeof api.addDatabaseLink>>);
  vi.mocked(api.addDatabaseBackup).mockResolvedValue({} as Awaited<ReturnType<typeof api.addDatabaseBackup>>);
  vi.mocked(api.restoreBackup).mockResolvedValue(db);
  vi.mocked(api.changeDatabaseLan).mockResolvedValue({ enabled: true, host: 'db.example', port: 15432, caCertificate: '', ufwCommand: null });
  vi.mocked(api.fetchDatabaseLan).mockResolvedValueOnce({ enabled: false, host: null, port: null, caCertificate: null, ufwCommand: null }).mockResolvedValueOnce({ enabled: true, host: 'db.example', port: 15432, caCertificate: '', ufwCommand: null }).mockResolvedValue({ enabled: false, host: null, port: null, caCertificate: null, ufwCommand: null });
  vi.mocked(api.removeDatabase).mockResolvedValue();
  render(<MemoryRouter initialEntries={['/databases/alice/db']}><Routes><Route path="/databases/:owner/:name" element={<><DatabaseDetailPage /><AppDatabases owner="alice" repo="r" app="web" /></>} /><Route path="/databases" element={<p>Deleted</p>} /></Routes></MemoryRouter>);
  await userEvent.selectOptions(await screen.findByLabelText('Database'), 'db');
  await userEvent.click(screen.getByRole('button', { name: 'Link database' }));
  await waitFor(() => expect(api.addDatabaseLink).toHaveBeenCalledWith('alice', 'db', { app: { owner: 'alice', repo: 'r', name: 'web' }, mode: 'rw' }));
  await userEvent.click(await screen.findByRole('button', { name: 'Create backup' }));
  await waitFor(() => expect(api.addDatabaseBackup).toHaveBeenCalled());
  await userEvent.type(screen.getByLabelText('Restore confirmation'), 'alice/db');
  await userEvent.click(screen.getByRole('button', { name: 'Restore' }));
  await waitFor(() => expect(api.restoreBackup).toHaveBeenCalledWith('alice', 'db', 'b1', { target: 'same', confirm: 'alice/db' }));
  await userEvent.click(await screen.findByRole('button', { name: 'Enable LAN access' }));
  await waitFor(() => expect(api.changeDatabaseLan).toHaveBeenCalledWith('alice', 'db', true));
  await userEvent.click(await screen.findByRole('button', { name: 'Disable LAN access' }));
  await waitFor(() => expect(api.changeDatabaseLan).toHaveBeenCalledWith('alice', 'db', false));
  await userEvent.type(screen.getByLabelText('Type alice/db to delete'), 'alice/db');
  await userEvent.click(screen.getByRole('button', { name: 'Delete database' }));
  expect(await screen.findByText('Deleted')).toBeInTheDocument();
}, 30000);
