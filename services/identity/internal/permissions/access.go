package permissions

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SourceKind è la provenienza di un ruolo su una risorsa.
type SourceKind string

const (
	// SourceDirect: grant all'utente.
	SourceDirect SourceKind = "direct"
	// SourceTeam: grant a un team di cui l'utente è membro, o di
	// un'organizzazione di cui è owner.
	SourceTeam SourceKind = "team"
	// SourceOwner: owner dell'organizzazione proprietaria (P1) o proprietario
	// del repo personale (P6): admin.
	SourceOwner SourceKind = "owner"
	// SourceInternal: risorsa interna (P3): read.
	SourceInternal SourceKind = "internal"
)

// ErrUserNotFound: l'utente di cui si chiede l'accesso non esiste.
var ErrUserNotFound = errors.New("utente non trovato")

// Source è una fonte del ruolo di un utente su una risorsa.
type Source struct {
	Kind         SourceKind
	Role         Role
	Organization string // team e owner di organizzazione
	Team         string // team
}

// ResourceAccess è l'accesso effettivo di un utente a una risorsa: il ruolo
// più alto fra le fonti, e le fonti.
type ResourceAccess struct {
	ResourceID uuid.UUID
	Role       Role
	Sources    []Source
}

// accessSQL elenca le fonti di accesso dell'utente $1 con le stesse regole di
// effectiveSQL/readableSQL: grant diretti, grant ai team di cui è membro,
// grant ai team delle organizzazioni di cui è owner, owner (P1, P6) e
// visibilità interna (P3). Un team raggiunto sia da membership sia da
// ownership dell'organizzazione compare una volta (UNION).
const accessSQL = `
SELECT g.resource_id, 'direct' AS kind, g.role, '' AS org_name, '' AS team_name
FROM identity.resource_grants g
WHERE g.user_id = $1
UNION
SELECT g.resource_id, 'team', g.role, o.name, t.name
FROM identity.resource_grants g
JOIN identity.teams t ON t.id = g.team_id
JOIN identity.organizations o ON o.id = t.org_id
WHERE g.team_id IN (SELECT tm.team_id FROM identity.team_members tm WHERE tm.user_id = $1)
   OR EXISTS (SELECT 1 FROM identity.org_members om
              WHERE om.org_id = t.org_id AND om.user_id = $1 AND om.role = 'owner')
UNION
SELECT a.resource_id, 'owner', 'admin', '', ''
FROM identity.resource_attributes a
WHERE a.owner_type = 'user' AND a.owner_id = $1
UNION
SELECT a.resource_id, 'owner', 'admin', o.name, ''
FROM identity.resource_attributes a
JOIN identity.organizations o ON o.id = a.owner_id
JOIN identity.org_members om ON om.org_id = o.id AND om.user_id = $1 AND om.role = 'owner'
WHERE a.owner_type = 'organization'
UNION
SELECT a.resource_id, 'internal', 'read', '', ''
FROM identity.resource_attributes a
WHERE a.visibility = 'internal'`

// UserAccess ritorna le risorse raggiungibili dall'utente con ruolo effettivo
// e fonti, ordinate per id. Per l'amministratore di sistema admin è vero e
// l'elenco è vuoto (ha admin su tutto: lo elenca chi conosce le risorse).
// Un utente disattivato non ha accesso (nessun elemento); uno inesistente è
// ErrUserNotFound.
func (s *Service) UserAccess(ctx context.Context, userID uuid.UUID) (admin bool, items []ResourceAccess, err error) {
	var isAdmin, active bool
	err = s.pool.QueryRow(ctx, `SELECT is_admin, is_active FROM identity.users WHERE id = $1`, userID).Scan(&isAdmin, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil, ErrUserNotFound
	}
	if err != nil {
		return false, nil, fmt.Errorf("lettura dell'utente non riuscita: %w", err)
	}
	items = []ResourceAccess{}
	if !active {
		return false, items, nil
	}
	if isAdmin {
		return true, items, nil
	}
	rows, err := s.pool.Query(ctx, accessSQL, userID)
	if err != nil {
		return false, nil, fmt.Errorf("calcolo dell'accesso dell'utente non riuscito: %w", err)
	}
	defer rows.Close()
	byRes := map[uuid.UUID]*ResourceAccess{}
	for rows.Next() {
		var id uuid.UUID
		var kind, role string
		var src Source
		if err := rows.Scan(&id, &kind, &role, &src.Organization, &src.Team); err != nil {
			return false, nil, fmt.Errorf("lettura di una fonte di accesso non riuscita: %w", err)
		}
		src.Kind, src.Role = SourceKind(kind), Role(role)
		ra := byRes[id]
		if ra == nil {
			ra = &ResourceAccess{ResourceID: id}
			byRes[id] = ra
		}
		ra.Role = Max(ra.Role, src.Role)
		ra.Sources = append(ra.Sources, src)
	}
	if err := rows.Err(); err != nil {
		return false, nil, fmt.Errorf("calcolo dell'accesso dell'utente non riuscito: %w", err)
	}
	for _, ra := range byRes {
		sort.Slice(ra.Sources, func(i, j int) bool {
			a, b := ra.Sources[i], ra.Sources[j]
			if a.Role.Rank() != b.Role.Rank() {
				return a.Role.Rank() > b.Role.Rank()
			}
			if a.Kind != b.Kind {
				return a.Kind < b.Kind
			}
			if a.Organization != b.Organization {
				return a.Organization < b.Organization
			}
			return a.Team < b.Team
		})
		items = append(items, *ra)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ResourceID.String() < items[j].ResourceID.String() })
	return false, items, nil
}
