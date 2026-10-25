// Permessi della scheda Apps (regola H5, senza ruoli nuovi): i pulsanti
// leggono tutti da qui, mai da un controllo sparso nella pagina.
//   read  : vede stato e storico dei deploy
//   write : deploy, riavvio, rollback, scala, legge i log
//   admin : crea ed elimina l'app, variabili, segreti, volumi, domini, accesso
// Un repo archiviato e' in sola lettura (R10): le app sono ferme e nessuna
// azione che cambia qualcosa e' offerta. L'admin dell'installazione vale admin.
export type AppRole = 'read' | 'write' | 'admin' | null;

export interface AppPermissions {
  /** Vede elenco, stato, versioni. */
  canView: boolean;
  /** Legge i log (write). */
  canReadLogs: boolean;
  /** Legge le variabili di configurazione (write), come da contratto H8. */
  canReadConfig: boolean;
  canDeploy: boolean;
  canRestart: boolean;
  canRollback: boolean;
  canScale: boolean;
  /** Crea una nuova app (admin). */
  canCreate: boolean;
  canDelete: boolean;
  /** Variabili, segreti, volumi e risorse (admin). */
  canConfigure: boolean;
  /** Modo di accesso e domini (admin). */
  canChangeAccess: boolean;
}

const NONE: AppPermissions = {
  canView: false,
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
};

export function appPermissions(role: AppRole, opts: { archived?: boolean; instanceAdmin?: boolean } = {}): AppPermissions {
  const effective: AppRole = opts.instanceAdmin ? 'admin' : role;
  if (effective === null) return NONE;
  const write = (effective === 'write' || effective === 'admin') && !opts.archived;
  const admin = effective === 'admin' && !opts.archived;
  return {
    canView: true,
    canReadLogs: write,
    canReadConfig: write,
    canDeploy: write,
    canRestart: write,
    canRollback: write,
    canScale: write,
    canCreate: admin,
    canDelete: admin,
    canConfigure: admin,
    canChangeAccess: admin,
  };
}
