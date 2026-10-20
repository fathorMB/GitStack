// Stati e durate delle esecuzioni (mockup B1/B2): funzioni pure, senza React.

export type RunTone = 'ok' | 'err' | 'run' | 'wait' | 'skip';

export interface RunState {
  tone: RunTone;
  label: string;
}

/** Stato = colore + icona + testo. status: queued, in_progress, completed, cancelled; conclusion quando conclusa. */
export function runState(status: string, conclusion?: string | null): RunState {
  if (status === 'queued') return { tone: 'wait', label: 'Queued' };
  if (status === 'in_progress') return { tone: 'run', label: 'Running' };
  if (status === 'cancelled') return { tone: 'skip', label: 'Cancelled' };
  switch (conclusion) {
    case 'success':
      return { tone: 'ok', label: 'Success' };
    case 'failure':
      return { tone: 'err', label: 'Failed' };
    case 'timed_out':
      return { tone: 'err', label: 'Timed out' };
    case 'cancelled':
      return { tone: 'skip', label: 'Cancelled' };
    case 'skipped':
      return { tone: 'skip', label: 'Skipped' };
    default:
      return { tone: 'skip', label: status || 'Unknown' };
  }
}

export function isActive(status: string): boolean {
  return status === 'queued' || status === 'in_progress';
}

/** 41m, 6m 12s, 48s. */
export function formatDuration(startedAt?: string | null, completedAt?: string | null, now: number = Date.now()): string {
  if (!startedAt) return '';
  const start = Date.parse(startedAt);
  if (Number.isNaN(start)) return '';
  const end = completedAt ? Date.parse(completedAt) : now;
  const s = Math.max(0, Math.round((end - start) / 1000));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  if (h > 0) return m > 0 ? `${h}h ${m}m` : `${h}h`;
  if (m > 0) return sec > 0 && m < 10 ? `${m}m ${sec}s` : `${m}m`;
  return `${sec}s`;
}

/** refs/heads/main -> main. */
export function shortRef(ref: string): string {
  return ref.replace(/^refs\/(heads|tags)\//, '');
}

/** Riga del log da evidenziare come errore (mockup: .er). */
export function logLineClass(line: string): 'er' | 'gr' | '' {
  if (/^(--- FAIL|FAIL\b|Error:|##\[error\])/.test(line)) return 'er';
  if (/^Run /.test(line)) return 'gr';
  return '';
}

export interface RunFilter {
  workflowId: string | null;
  quick: 'all' | 'failed' | 'running';
  text: string;
}

interface Filterable {
  workflowId: string;
  status: string;
  conclusion?: string | null;
  event: string;
  ref: string;
}

/** Filtro delle esecuzioni, lato client: l'elenco dell'API non ha parametri. Chiavi: branch:, event:, status:. */
export function filterRuns<T extends Filterable>(runs: T[], f: RunFilter): T[] {
  const terms = f.text.trim().split(/\s+/).filter(Boolean);
  return runs.filter((r) => {
    if (f.workflowId && r.workflowId !== f.workflowId) return false;
    const st = runState(r.status, r.conclusion);
    if (f.quick === 'failed' && st.tone !== 'err') return false;
    if (f.quick === 'running' && !isActive(r.status)) return false;
    for (const t of terms) {
      const m = /^(branch|event|status):(.*)$/i.exec(t);
      if (!m) continue;
      const v = m[2]!.toLowerCase();
      if (!v) continue;
      const key = m[1]!.toLowerCase();
      if (key === 'branch' && shortRef(r.ref).toLowerCase() !== v) return false;
      if (key === 'event' && r.event.toLowerCase() !== v) return false;
      if (key === 'status' && st.label.toLowerCase() !== v && r.status.toLowerCase() !== v && (r.conclusion ?? '').toLowerCase() !== v) return false;
    }
    return true;
  });
}
