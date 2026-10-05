import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it } from 'vitest';
import type { FileDiff } from '@gitstack/api-client';
import { parsePatch, toSplitRows } from '../lib/diffParse';
import { DiffView } from './DiffView';
import type { DiffViewProps } from './DiffView';

const PATCH = ['@@ -1,3 +1,3 @@', ' keep', '-old one', '-old two', '+new one', '+new two', '+new three', ' tail'].join('\n') + '\n';

const file = (over: Partial<FileDiff>): FileDiff => ({ path: 'a.go', status: 'modified', additions: 3, deletions: 2, binary: false, truncated: false, patch: PATCH, ...over });

function show(files: FileDiff[], props: Partial<DiffViewProps> = {}) {
  render(
    <MemoryRouter>
      <DiffView files={files} fileHref={(f) => `/acme/api/blob/abc/${f.path}`} {...props} />
    </MemoryRouter>,
  );
}

describe('parsePatch / toSplitRows', () => {
  it('numera le righe da hunk', () => {
    const lines = parsePatch(PATCH);
    expect(lines.map((l) => l.kind)).toEqual(['hunk', 'ctx', 'del', 'del', 'add', 'add', 'add', 'ctx']);
    expect(lines[1]).toMatchObject({ oldNo: 1, newNo: 1, text: 'keep' });
    expect(lines[3]).toMatchObject({ oldNo: 3, text: 'old two' });
    expect(lines[6]).toMatchObject({ newNo: 4, text: 'new three' });
    expect(lines[7]).toMatchObject({ oldNo: 4, newNo: 5 });
  });

  it('affianca i blocchi del/add riga per riga', () => {
    const rows = toSplitRows(parsePatch(PATCH));
    expect(rows).toHaveLength(1 + 1 + 3 + 1);
    expect(rows[2]?.left?.text).toBe('old one');
    expect(rows[2]?.right?.text).toBe('new one');
    expect(rows[4]?.left).toBeNull();
    expect(rows[4]?.right?.text).toBe('new three');
  });
});

describe('DiffView', () => {
  it('unificato: righe aggiunte e tolte con i prefissi e il conteggio', () => {
    show([file({})]);
    const t = screen.getByRole('table', { name: 'Unified diff of a.go' });
    expect(within(t).getAllByRole('row')).toHaveLength(8);
    expect(within(t).getByText('+new one')).toBeInTheDocument();
    expect(within(t).getByText('-old one')).toBeInTheDocument();
    expect(screen.getByText('+3 −2')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'View file' })).toHaveAttribute('href', '/acme/api/blob/abc/a.go');
  });

  it('affiancato: due colonne senza prefissi', () => {
    show([file({})], { mode: 'split' });
    const t = screen.getByRole('table', { name: 'Split diff of a.go' });
    expect(within(t).getAllByText('old one')).toHaveLength(1);
    expect(within(t).getByText('new three')).toBeInTheDocument();
    expect(screen.queryByRole('table', { name: 'Unified diff of a.go' })).toBeNull();
  });

  it('file chiuso: motivo e Load diff aprono il patch', async () => {
    show([file({ path: 'go.sum', collapsed: true, collapseReason: 'lock' })]);
    expect(screen.getByText('Lock file')).toBeInTheDocument();
    expect(screen.getByText('Lock and generated files are collapsed by default.')).toBeInTheDocument();
    expect(screen.queryByRole('table')).toBeNull();
    await userEvent.click(screen.getByRole('button', { name: 'Load diff' }));
    expect(screen.getByRole('table', { name: 'Unified diff of go.sum' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Load diff' })).toBeNull();
  });

  it('motivi large e generated', () => {
    show([file({ path: 'big.go', collapsed: true, collapseReason: 'large' }), file({ path: 'x.pb.go', collapsed: true, collapseReason: 'generated' })]);
    expect(screen.getByText('Large diff')).toBeInTheDocument();
    expect(screen.getByText('Generated')).toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: 'Load diff' })).toHaveLength(2);
  });

  it('il corpo si chiude e si riapre', async () => {
    show([file({})]);
    await userEvent.click(screen.getByRole('button', { name: 'Collapse a.go' }));
    expect(screen.queryByRole('table')).toBeNull();
    await userEvent.click(screen.getByRole('button', { name: 'Expand a.go' }));
    expect(screen.getByRole('table')).toBeInTheDocument();
  });

  it('binario e rinominato', () => {
    show([file({ path: 'logo.png', binary: true, patch: undefined }), file({ path: 'new/name.go', oldPath: 'old/name.go', status: 'renamed', additions: 0, deletions: 0, patch: undefined })]);
    expect(screen.getByText('Binary file not shown.')).toBeInTheDocument();
    expect(screen.getByText('old/name.go')).toBeInTheDocument();
    expect(screen.getByText('Renamed')).toBeInTheDocument();
    expect(screen.getByText('File moved without changes.')).toBeInTheDocument();
  });

  it('un file eliminato non ha View file', () => {
    show([file({ status: 'deleted' })]);
    expect(screen.queryByRole('link', { name: 'View file' })).toBeNull();
  });

  it('listOnly: solo elenco con le righe, niente patch ne Load diff', () => {
    show([file({ patch: undefined, collapsed: true, collapseReason: 'large' }), file({ path: 'b.go', patch: undefined })], { listOnly: true });
    expect(screen.queryByRole('table')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Load diff' })).toBeNull();
    expect(screen.getAllByText('+3 −2')).toHaveLength(2);
    expect(screen.getByText('b.go')).toBeInTheDocument();
  });
});
