import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Resource, ResourceList } from '@gitstack/api-client';
import { ResourcesPage } from './ResourcesPage';

// Il componente parla solo con './lib/resourcesApi' (il wrapper del client
// generato, T-03): qui si isola quel modulo, non fetch/rete, per restare un
// test di componente, non un test end-to-end (quello lo fa M-01 a livello
// di sistema).
vi.mock('../lib/resourcesApi', () => ({
  ApiError: class ApiError extends Error {},
  fetchResources: vi.fn(),
  createTestResource: vi.fn(),
}));

import { createTestResource, fetchResources } from '../lib/resourcesApi';

const mockedFetchResources = vi.mocked(fetchResources);
const mockedCreateTestResource = vi.mocked(createTestResource);

function resource(overrides: Partial<Resource> = {}): Resource {
  return {
    id: 'r-1',
    type: 'demo',
    name: 'prova',
    createdAt: '2026-09-28T00:00:00Z',
    updatedAt: '2026-09-28T00:00:00Z',
    ...overrides,
  };
}

function resourceList(items: Resource[]): ResourceList {
  return { items, page: 1, perPage: 20, total: items.length };
}

describe('ResourcesPage', () => {
  beforeEach(() => {
    mockedFetchResources.mockReset();
    mockedCreateTestResource.mockReset();
  });

  it('elenca le risorse ricevute dal gateway tramite il client generato', async () => {
    mockedFetchResources.mockResolvedValue(resourceList([resource({ id: 'r-1', name: 'demo-1' })]));

    render(<ResourcesPage />);

    expect(await screen.findByText('demo-1')).toBeInTheDocument();
    expect(mockedFetchResources).toHaveBeenCalledTimes(1);
  });

  it('mostra lo stato vuoto quando non ci sono risorse', async () => {
    mockedFetchResources.mockResolvedValue(resourceList([]));

    render(<ResourcesPage />);

    expect(await screen.findByText('No resources yet')).toBeInTheDocument();
  });

  it('crea una risorsa con il client generato e la aggiunge alla lista', async () => {
    mockedFetchResources.mockResolvedValue(resourceList([]));
    mockedCreateTestResource.mockResolvedValue(resource({ id: 'r-2', name: 'nuova-risorsa' }));

    const user = userEvent.setup();
    render(<ResourcesPage />);

    await screen.findByText('No resources yet');

    await user.type(screen.getByLabelText('Name'), 'nuova-risorsa');
    await user.click(screen.getByRole('button', { name: /create resource/i }));

    await waitFor(() => {
      expect(mockedCreateTestResource).toHaveBeenCalledWith({ type: 'demo', name: 'nuova-risorsa' });
    });
    expect(await screen.findByText('nuova-risorsa')).toBeInTheDocument();
  });
});
