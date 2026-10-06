import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { CodeSearchResult, CommitSummary, Tag } from '../../lib/codeApi';
import type { Repository } from '../../lib/reposApi';
import { RepoPage } from './RepoPage';
import { Highlighted } from './SearchPage';

vi.mock('../../lib/reposApi', async (orig) => ({ ...(await orig<typeof import('../../lib/reposApi')>()), fetchRepo: vi.fn() }));
vi.mock('../../lib/authApi', () => ({ fetchSession: vi.fn() }));
vi.mock('../../lib/orgsApi', () => ({ fetchOrgMembers: vi.fn() }));
vi.mock('../../lib/codeApi', async (orig) => ({
  ...(await orig<typeof import('../../lib/codeApi')>()),
  fetchTags: vi.fn(),
  searchCode: vi.fn(),
}));

import { fetchSession } from '../../lib/authApi';
import { fetchTags, searchCode } from '../../lib/codeApi';
import { fetchRepo } from '../../lib/reposApi';

const repo = (): Repository =>
  ({
    id: 'r1',
    owner: { type: 'organization', name: 'acme' },
    name: 'api',
    fullName: 'acme/api',
    description: '',
    visibility: 'private',
    defaultBranch: 'main',
    archived: false,
    empty: false,
    cloneUrls: { https: 'h', ssh: 's' },
  }) as Repository;

function commit(sha: string): CommitSummary {
  const person = { name: 'Build', email: 'b@x', date: '2026-09-27T10:00:00Z' };
  return { sha: sha.padEnd(40, '0'), subject: 's', author: person, committer: person, parents: [] } as CommitSummary;
}

const tag = (name: string, sha: string, taggedAt: string, message?: string): Tag => ({
  name,
  annotated: message !== undefined,
  ...(message !== undefined ? { message } : {}),
  taggedAt,
  commit: commit(sha),
});

function Where() {
  const l = useLocation();
  return <div data-testid="where">{l.pathname + l.search + l.hash}</div>;
}

function renderAt(path: string) {
  render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/:owner/:repo/tags" element={<RepoPage mode="tags" />} />
        <Route path="/:owner/:repo/search" element={<RepoPage mode="search" />} />
        <Route path="/:owner/:repo/blob/*" element={<Where />} />
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(fetchSession).mockResolvedValue({ user: { username: 'nobody', isAdmin: false } } as never);
  vi.mocked(fetchRepo).mockResolvedValue(repo());
});

describe('pagina Tags (mockup 23)', () => {
  beforeEach(() => {
    vi.mocked(fetchTags).mockResolvedValue({
      items: [
        tag('v0.4.0', '77ad0e1', '2026-08-01T10:00:00Z'),
        tag('v0.4.2', '4e1a9c0', '2026-10-02T10:00:00Z', 'Fix SSH host keys'),
      ],
      total: 2,
    });
  });

  it('mostra il tag annotato col messaggio e quello leggero senza, il piu recente per primo con Latest', async () => {
    renderAt('/acme/api/tags');
    const rows = await screen.findAllByRole('listitem');
    expect(rows).toHaveLength(2);
    expect(within(rows[0]!).getByText('v0.4.2')).toBeInTheDocument();
    expect(within(rows[0]!).getByText('Latest')).toBeInTheDocument();
    expect(within(rows[0]!).getByText('Fix SSH host keys')).toBeInTheDocument();
    expect(within(rows[0]!).getByRole('link', { name: '4e1a9c0' })).toHaveAttribute('href', '/acme/api/commit/' + '4e1a9c0'.padEnd(40, '0'));
    expect(within(rows[1]!).getByText('v0.4.0')).toBeInTheDocument();
    expect(within(rows[1]!).getByText('Lightweight tag (no message)')).toBeInTheDocument();
    expect(within(rows[1]!).queryByText('Latest')).toBeNull();
    expect(screen.getByText('2', { selector: '.counter' })).toBeInTheDocument();
    const note = screen.getByRole('note');
    expect(note).toHaveTextContent('/api/v1/repos/acme/api/archive?ref=v0.4.2&format=tar.gz');
    expect(note.textContent).not.toContain('/acme/api/archive/');
    expect(note).toHaveTextContent('-o api-v0.4.2.tar.gz');
  });

  it('offre il download ZIP e tar.gz di ogni tag', async () => {
    renderAt('/acme/api/tags');
    const zip = await screen.findByRole('link', { name: 'Download v0.4.2 as ZIP' });
    expect(zip).toHaveAttribute('href', expect.stringMatching(/\/repos\/acme\/api\/archive\?ref=v0\.4\.2&format=zip$/));
    expect(screen.getByRole('link', { name: 'Download v0.4.2 as tar.gz' })).toHaveAttribute('href', expect.stringMatching(/format=tar\.gz$/));
    expect(screen.getByRole('link', { name: 'Download v0.4.0 as ZIP' })).toBeInTheDocument();
  });

  it('filtra per nome e segnala quando nessun tag corrisponde', async () => {
    renderAt('/acme/api/tags');
    await userEvent.type(await screen.findByLabelText('Find a tag'), '0.4.0');
    expect(screen.getAllByRole('listitem')).toHaveLength(1);
    await userEvent.type(screen.getByLabelText('Find a tag'), 'zzz');
    expect(screen.getByText('No tags match your search.')).toBeInTheDocument();
  });

  it('senza tag lo dice', async () => {
    vi.mocked(fetchTags).mockResolvedValue({ items: [], total: 0 });
    renderAt('/acme/api/tags');
    expect(await screen.findByRole('heading', { name: 'No tags yet' })).toBeInTheDocument();
    expect(screen.getByText('Create a tag with git tag and publish it with git push origin --tags.')).toBeInTheDocument();
    expect(screen.queryByRole('list', { name: 'Tags' })).not.toBeInTheDocument();
  });
});

describe('risultati di Search code (B5)', () => {
  const result = (over: Partial<CodeSearchResult> = {}): CodeSearchResult => ({
    ref: 'main',
    query: 'handler',
    results: [
      { path: 'cmd/main.go', line: 12, fragment: 'func Handler() {' },
      { path: 'cmd/main.go', line: 40, fragment: 'r.Handle(handler)' },
      { path: 'README.md', line: 3, fragment: 'the handler docs' },
    ],
    limitReached: false,
    timedOut: false,
    ...over,
  });

  it('elenca percorso, riga e frammento evidenziato con link alla riga della vista file', async () => {
    vi.mocked(searchCode).mockResolvedValue(result());
    renderAt('/acme/api/search?q=handler&ref=main');
    expect(await screen.findByText(/3 results/)).toBeInTheDocument();
    expect(searchCode).toHaveBeenCalledWith('acme', 'api', 'handler', 'main');
    const section = screen.getByRole('region', { name: 'cmd/main.go' });
    const link = within(section).getByRole('link', { name: 'cmd/main.go line 12' });
    expect(link).toHaveAttribute('href', '/acme/api/blob/main/cmd/main.go#L12');
    const marks = within(section).getAllByText(/handler/i, { selector: 'mark' });
    expect(marks.map((m) => m.textContent)).toEqual(['Handler', 'handler']);
    expect(screen.queryByRole('status')).toBeNull();
    await userEvent.click(link);
    expect(screen.getByTestId('where')).toHaveTextContent('/acme/api/blob/main/cmd/main.go#L12');
  });

  it('senza risultati lo dice', async () => {
    vi.mocked(searchCode).mockResolvedValue(result({ results: [] }));
    renderAt('/acme/api/search?q=nulla&ref=main');
    expect(await screen.findByRole('heading', { name: 'No results for “nulla”' })).toBeInTheDocument();
    expect(screen.getByText('Try a different search term or another branch or tag.')).toBeInTheDocument();
    expect(screen.queryByRole('status')).toBeNull();
  });

  it('avvisa quando i risultati sono limitati a 100', async () => {
    const many = Array.from({ length: 100 }, (_, i) => ({ path: 'a.txt', line: i + 1, fragment: 'handler' }));
    vi.mocked(searchCode).mockResolvedValue(result({ results: many, limitReached: true }));
    renderAt('/acme/api/search?q=handler&ref=main');
    expect(await screen.findByRole('status')).toHaveTextContent('Results limited to the first 100');
  });

  it('avvisa quando la ricerca e andata in timeout', async () => {
    vi.mocked(searchCode).mockResolvedValue(result({ timedOut: true }));
    renderAt('/acme/api/search?q=handler&ref=main');
    expect(await screen.findByRole('status')).toHaveTextContent('timed out');
  });

  it('una nuova ricerca dalla casella aggiorna indirizzo e richiesta, sullo stesso ref', async () => {
    vi.mocked(searchCode).mockResolvedValue(result());
    renderAt('/acme/api/search?q=handler&ref=v1.0');
    await screen.findByText(/3 results/);
    const input = screen.getByLabelText('Search query');
    await userEvent.clear(input);
    await userEvent.type(input, 'router{enter}');
    expect(searchCode).toHaveBeenLastCalledWith('acme', 'api', 'router', 'v1.0');
  });

  it('con meno di 2 caratteri non chiama l API', async () => {
    renderAt('/acme/api/search?q=a&ref=main');
    expect(await screen.findByText(/at least 2 characters/)).toBeInTheDocument();
    expect(searchCode).not.toHaveBeenCalled();
  });
});

describe('Highlighted', () => {
  it('evidenzia tutte le occorrenze senza distinguere maiuscole', () => {
    const { container } = render(<Highlighted text="Foo foo bar" q="foo" />);
    expect([...container.querySelectorAll('mark')].map((m) => m.textContent)).toEqual(['Foo', 'foo']);
  });
});
