import { describe, expect, it } from 'vitest';
import { appPermissions } from './appsPermissions';

// Regola H5: read vede; write fa deploy, riavvio, rollback, scala e legge i log;
// admin crea/elimina e gestisce configurazione e accesso.
describe('appPermissions (H5)', () => {
  it('senza ruolo non c e niente, nemmeno la vista', () => {
    const p = appPermissions(null);
    expect(Object.values(p).every((v) => v === false)).toBe(true);
  });

  it('read vede e basta', () => {
    expect(appPermissions('read')).toEqual({
      canView: true,
      canReadLogs: false,
      canReadConfig: false,
      canDeploy: false,
      canRestart: false,
      canRollback: false,
      canScale: false,
      canCreate: false,
      canDelete: false,
      canConfigure: false,
      canChangeAccess: false,
    });
  });

  it('write opera sull app ma non la crea, non la configura, non cambia l accesso', () => {
    const p = appPermissions('write');
    expect(p.canReadConfig).toBe(true);
    expect([p.canView, p.canReadLogs, p.canDeploy, p.canRestart, p.canRollback, p.canScale]).toEqual([true, true, true, true, true, true]);
    expect([p.canCreate, p.canDelete, p.canConfigure, p.canChangeAccess]).toEqual([false, false, false, false]);
  });

  it('admin ha tutto', () => {
    expect(Object.values(appPermissions('admin')).every((v) => v === true)).toBe(true);
  });

  it('l admin dell installazione vale admin anche senza ruolo sul repo', () => {
    expect(appPermissions(null, { instanceAdmin: true }).canCreate).toBe(true);
    expect(appPermissions('read', { instanceAdmin: true }).canChangeAccess).toBe(true);
  });

  it('repo archiviato: solo lettura, anche per admin', () => {
    const p = appPermissions('admin', { archived: true });
    expect(p.canView).toBe(true);
    expect(Object.entries(p).filter(([k, v]) => k !== 'canView' && v)).toEqual([]);
  });
});
