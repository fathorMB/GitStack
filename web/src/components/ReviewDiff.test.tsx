import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { FileDiff } from '@gitstack/api-client';
import type { PullRequestComment, PullRequestThread } from '../lib/pullsApi';
import { ReviewDiff } from './ReviewDiff';
import type { ReviewDiffProps } from './ReviewDiff';

const PATCH = ['@@ -10,3 +10,5 @@', ' ctx a', '-old b', '+new b', '+new c', '+new d', ' ctx e', ''].join('\n');

const file = (over: Partial<FileDiff> = {}): FileDiff => ({
  path: 'internal/deliver.go',
  status: 'modified',
  additions: 3,
  deletions: 1,
  binary: false,
  truncated: false,
  patch: PATCH,
  ...over,
});

const lb = { id: 'u1', username: 'lbianchi', kind: 'human' as const };
const bot = { id: 'u2', username: 'build-agent', kind: 'agent' as const };

const comment = (over: Partial<PullRequestComment> = {}): PullRequestComment => ({
  id: 'c1',
  author: lb,
  body: 'Should 404 stop too?',
  outdated: false,
  edited: false,
  deleted: false,
  createdAt: '2026-10-18T10:00:00Z',
  updatedAt: '2026-10-18T10:00:00Z',
  ...over,
});

const thread = (over: Partial<PullRequestThread> = {}): PullRequestThread => ({
  id: 'c1',
  path: 'internal/deliver.go',
  side: 'new',
  line: 13,
  startLine: 11,
  resolved: false,
  outdated: false,
  comments: [comment()],
  ...over,
});

function setup(over: Partial<ReviewDiffProps> = {}, f: FileDiff = file()) {
  const props: ReviewDiffProps = {
    files: [f],
    mode: 'unified',
    threads: [],
    viewed: new Set<string>(),
    canComment: true,
    canResolve: true,
    onViewed: vi.fn(),
    onComment: vi.fn().mockResolvedValue(true),
    onReply: vi.fn().mockResolvedValue(true),
    onResolve: vi.fn(),
    ...over,
  };
  render(<ReviewDiff {...props} />);
  return props;
}

describe('ReviewDiff · commenti sulle righe', () => {
  it('commenta una riga sola', () => {
    const p = setup();
    fireEvent.click(screen.getByRole('button', { name: 'Comment on line 12' }));
    fireEvent.change(screen.getByRole('textbox', { name: 'Comment on line +12' }), { target: { value: 'Why c?' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add comment' }));
    expect(p.onComment).toHaveBeenCalledWith({ path: 'internal/deliver.go', side: 'new', line: 12, body: 'Why c?' });
  });

  it('commenta un gruppo di righe con shift-click', () => {
    const p = setup();
    fireEvent.click(screen.getByRole('button', { name: 'Comment on line 11' }));
    fireEvent.click(screen.getByRole('button', { name: 'Comment on line 13' }), { shiftKey: true });
    expect(screen.getByRole('group', { name: 'Comment on lines +11 to +13' })).toBeInTheDocument();
    fireEvent.change(screen.getByRole('textbox', { name: 'Comment on lines +11 to +13' }), { target: { value: 'Rewrite this block' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add comment' }));
    expect(p.onComment).toHaveBeenCalledWith({ path: 'internal/deliver.go', side: 'new', line: 13, startLine: 11, body: 'Rewrite this block' });
  });

  it('una riga tolta si commenta sul lato old', () => {
    const p = setup();
    fireEvent.click(screen.getByRole('button', { name: 'Comment on old line 11' }));
    fireEvent.change(screen.getByRole('textbox', { name: 'Comment on line −11' }), { target: { value: 'Why removed?' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add comment' }));
    expect(p.onComment).toHaveBeenCalledWith({ path: 'internal/deliver.go', side: 'old', line: 11, body: 'Why removed?' });
  });

  it('«Suggest a change» precompila il blocco con le righe scelte', () => {
    setup();
    fireEvent.click(screen.getByRole('button', { name: 'Comment on line 12' }));
    fireEvent.click(screen.getByRole('button', { name: 'Suggest a change' }));
    expect((screen.getByRole('textbox', { name: 'Comment on line +12' }) as HTMLTextAreaElement).value).toBe('```suggestion\nnew c\n```\n');
  });

  it('senza permesso di commentare non ci sono pulsanti sulle righe', () => {
    setup({ canComment: false });
    expect(screen.queryByRole('button', { name: /Comment on/ })).toBeNull();
  });

  it('nella vista affiancata si commenta a destra', () => {
    const p = setup({ mode: 'split' });
    expect(screen.getByRole('table', { name: 'Split diff of internal/deliver.go' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Comment on line 12' }));
    fireEvent.change(screen.getByRole('textbox', { name: 'Comment on line +12' }), { target: { value: 'ok' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add comment' }));
    expect(p.onComment).toHaveBeenCalledWith(expect.objectContaining({ side: 'new', line: 12 }));
  });
});

describe('ReviewDiff · conversazioni', () => {
  it('mostra la conversazione sotto l ultima riga e permette risposta e risoluzione', () => {
    const p = setup({ threads: [thread({ comments: [comment(), comment({ id: 'c2', author: bot, body: 'Left it out.' })] })] });
    const t = screen.getByRole('group', { name: 'Conversation on internal/deliver.go lines +11 to +13' });
    expect(within(t).getByText('Comment on lines +11 to +13')).toBeInTheDocument();
    expect(within(t).getByText('agent')).toBeInTheDocument();
    fireEvent.change(within(t).getByRole('textbox', { name: 'Reply to lbianchi' }), { target: { value: 'Thanks' } });
    fireEvent.click(within(t).getByRole('button', { name: 'Reply' }));
    expect(p.onReply).toHaveBeenCalledWith('c1', 'Thanks');
    fireEvent.click(within(t).getByRole('button', { name: 'Resolve conversation' }));
    expect(p.onResolve).toHaveBeenCalledWith('c1', true);
  });

  it('una conversazione risolta e compressa e si riapre', () => {
    const p = setup({ threads: [thread({ resolved: true, resolvedBy: lb })] });
    expect(screen.queryByText('Should 404 stop too?')).toBeNull();
    expect(screen.getByText('Resolved')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Show conversation' }));
    expect(screen.getByText('Should 404 stop too?')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Unresolve conversation' }));
    expect(p.onResolve).toHaveBeenCalledWith('c1', false);
    fireEvent.click(screen.getByRole('button', { name: 'Hide conversation' }));
    expect(screen.queryByText('Should 404 stop too?')).toBeNull();
  });

  it('chi non puo risolvere non vede il pulsante', () => {
    setup({ canResolve: false, threads: [thread()] });
    expect(screen.queryByRole('button', { name: /Resolve conversation/ })).toBeNull();
  });

  it('un blocco suggestion e un diff, senza Apply', async () => {
    setup({
      threads: [thread({ line: 13, startLine: 13, comments: [comment({ body: 'Make it explicit:\n\n```suggestion\ncase x:\n    return y\n```\n' })] })],
    });
    expect(await screen.findByText('Make it explicit:')).toBeInTheDocument();
    const sg = screen.getByRole('group', { name: 'Suggested change' });
    expect(within(sg).getByText('-new d')).toBeInTheDocument();
    expect(within(sg).getByText('+case x:')).toBeInTheDocument();
    expect(within(sg).getByText((_, el) => el?.textContent === '+    return y' && el.children.length === 0)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /apply/i })).toBeNull();
    expect(within(sg).getByText('Apply it with a commit')).toBeInTheDocument();
  });

  it('una conversazione outdated sta in cima, compressa, con la sua etichetta', () => {
    setup({ threads: [thread({ outdated: true, resolved: true, line: 99, startLine: null })] });
    expect(screen.getByText('Outdated')).toBeInTheDocument();
    expect(screen.getByText(/on a line that changed in a later commit/)).toBeInTheDocument();
    expect(screen.queryByText('Should 404 stop too?')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Show conversation' }));
    expect(screen.getByText('Should 404 stop too?')).toBeInTheDocument();
  });
});

describe('ReviewDiff · Viewed e limiti', () => {
  it('la casella Viewed avvisa chi la usa', () => {
    const p = setup();
    const sec = screen.getByRole('region', { name: 'Diff of internal/deliver.go' });
    fireEvent.click(within(sec).getByRole('checkbox', { name: 'Viewed' }));
    expect(p.onViewed).toHaveBeenCalledWith('internal/deliver.go', true);
  });

  it('un file Viewed mostra l avviso e Show diff riapre il diff', () => {
    const p = setup({ viewed: new Set(['internal/deliver.go']) });
    expect(screen.getByText(/File marked as viewed/)).toBeInTheDocument();
    expect(screen.queryByRole('table')).toBeNull();
    expect(screen.getByRole('checkbox', { name: 'Viewed' })).toBeChecked();
    fireEvent.click(screen.getByRole('button', { name: 'Show diff' }));
    expect(screen.getByRole('table', { name: 'Unified diff of internal/deliver.go' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Viewed' }));
    expect(p.onViewed).toHaveBeenCalledWith('internal/deliver.go', false);
  });

  it('limiti B6: file chiuso con Load diff', () => {
    setup({}, file({ collapsed: true, collapseReason: 'large' }));
    expect(screen.getByText('Large diffs (over 500 changed lines) are collapsed by default.')).toBeInTheDocument();
    expect(screen.queryByRole('table')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Load diff' }));
    expect(screen.getByRole('table')).toBeInTheDocument();
  });

  it('listOnly: niente righe', () => {
    setup({ listOnly: true });
    expect(screen.queryByRole('table')).toBeNull();
    expect(screen.getByText('internal/deliver.go')).toBeInTheDocument();
  });

  it('un file binario non mostra righe', () => {
    setup({}, file({ binary: true, patch: undefined }));
    expect(screen.getByText('Binary file not shown.')).toBeInTheDocument();
  });
});
