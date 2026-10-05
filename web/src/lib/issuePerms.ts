import type { Issue, Role } from './issuesApi';

/** Cosa puo' fare il chiamante (I3, I4, I11, R10): calcolato dal ruolo effettivo, mai indovinato. */
export function permissions(issue: Issue, role: Role, me: string, archived: boolean, isSystemAdmin = false) {
  const admin = role === 'admin' || isSystemAdmin;
  const write = role === 'write' || admin;
  const author = issue.author.username === me;
  const live = !archived;
  return {
    admin,
    write,
    author,
    readOnly: archived,
    canEdit: live && author && (!issue.locked || write),
    canCloseReopen: live && (write || author),
    canComment: live && role !== null && (!issue.locked || write),
    canSidebar: live && write,
    canAdmin: live && admin,
  };
}
