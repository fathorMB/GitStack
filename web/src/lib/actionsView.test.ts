import { describe, expect, it } from 'vitest';
import { filterRuns, formatDuration, logLineClass, runState, shortRef } from './actionsView';

describe('actionsView', () => {
  it('mappa stato e conclusione', () => {
    expect(runState('queued')).toEqual({ tone: 'wait', label: 'Queued' });
    expect(runState('in_progress').tone).toBe('run');
    expect(runState('completed', 'failure')).toEqual({ tone: 'err', label: 'Failed' });
    expect(runState('completed', 'success').tone).toBe('ok');
    expect(runState('cancelled').label).toBe('Cancelled');
  });

  it('durate e ref', () => {
    expect(formatDuration('2026-01-01T00:00:00Z', '2026-01-01T00:06:12Z')).toBe('6m 12s');
    expect(formatDuration('2026-01-01T00:00:00Z', '2026-01-01T00:00:48Z')).toBe('48s');
    expect(formatDuration('2026-01-01T00:00:00Z', '2026-01-01T00:41:00Z')).toBe('41m');
    expect(formatDuration(null, null)).toBe('');
    expect(shortRef('refs/heads/feat/x')).toBe('feat/x');
  });

  it('righe di log', () => {
    expect(logLineClass('--- FAIL: TestX')).toBe('er');
    expect(logLineClass('Run go test')).toBe('gr');
    expect(logLineClass('ok pkg 1s')).toBe('');
  });

  it('filtri per chiave', () => {
    const r = [
      { workflowId: 'a', status: 'completed', conclusion: 'failure', event: 'push', ref: 'refs/heads/main' },
      { workflowId: 'b', status: 'in_progress', conclusion: null, event: 'schedule', ref: 'refs/heads/dev' },
    ];
    const f = { workflowId: null, quick: 'all' as const, text: '' };
    expect(filterRuns(r, { ...f, text: 'branch:dev' })).toHaveLength(1);
    expect(filterRuns(r, { ...f, text: 'event:push status:failure' })).toHaveLength(1);
    expect(filterRuns(r, { ...f, quick: 'running' })[0]?.workflowId).toBe('b');
  });
});
