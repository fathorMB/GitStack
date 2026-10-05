import { fetchSession } from '../../lib/authApi';
import type { User } from '../../lib/authApi';
import { fetchOrgMembers } from '../../lib/orgsApi';
import type { Repository } from '../../lib/reposApi';

// Il contratto non espone il ruolo dell'utente sul repo: «admin» si deduce
// come nel backend (P1, P6) da quello che il client puo' leggere: admin
// dell'installazione, proprietario del repo personale, owner
// dell'organizzazione. I grant admin sul singolo repo non sono visibili: in
// quel caso la sezione resta nascosta, e il backend risponde comunque 403.
export async function loadMe(): Promise<User> {
  return (await fetchSession()).user;
}

export async function isRepoAdmin(repo: Pick<Repository, 'owner'>, me: User): Promise<boolean> {
  if (me.isAdmin === true) return true;
  if (repo.owner.type === 'user') return repo.owner.name === me.username;
  try {
    const members = await fetchOrgMembers(repo.owner.name);
    return members.some((m) => m.user.username === me.username && m.role === 'owner');
  } catch {
    return false;
  }
}
