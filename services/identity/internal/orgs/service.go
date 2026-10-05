// Package orgs implementa organizzazioni, team, membership e grant
// sulle tabelle identity.organizations, org_members, teams, team_members.
package orgs

import (
	"github.com/fathorMB/GitStack/pkg/names"
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Errori di dominio.
var (
	ErrNotFound     = errors.New("organizzazione non trovata")
	ErrLastOwner    = errors.New("non si può rimuovere o declassare l'ultimo owner")
	ErrNotOrgMember = errors.New("utente non membro dell'organizzazione")
	ErrInvalidRole  = errors.New("ruolo non valido")
)

// ValidationError raccoglie i motivi di rifiuto per campo (422).
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Fields))
	for k, v := range e.Fields {
		parts = append(parts, k+": "+v)
	}
	return "validazione non riuscita: " + join(parts, "; ")
}

// Organization è una riga di identity.organizations.
type Organization struct {
	ID          uuid.UUID
	Name        string
	DisplayName string
	Description string
	CreatedAt   time.Time
}

// CreateOrgInput sono i dati per la creazione.
type CreateOrgInput struct {
	Name        string
	DisplayName string
	Description string
}

// UpdateOrgInput descrive un aggiornamento (almeno un campo non nil).
type UpdateOrgInput struct {
	DisplayName *string
	Description *string
}

// OrgMember è un membro di un'organizzazione.
type OrgMember struct {
	UserID    uuid.UUID
	Role      string // "owner" o "member"
	CreatedAt time.Time
}

// SetOrgMemberInput è il ruolo da assegnare (PUT).
type SetOrgMemberInput struct {
	Role string // "owner" o "member"
}

// Team è una riga di identity.teams.
type Team struct {
	ID          uuid.UUID
	OrgID       uuid.UUID
	Name        string
	Description string
	CreatedAt   time.Time
}

// CreateTeamInput sono i dati per la creazione di un team.
type CreateTeamInput struct {
	Name        string
	Description string
}

// UpdateTeamInput descrive un aggiornamento di un team.
type UpdateTeamInput struct {
	Name        *string
	Description *string
}

// TeamMember è un membro di un team.
type TeamMember struct {
	UserID    uuid.UUID
	Role      string // "member" o "maintainer"
	CreatedAt time.Time
}

// SetTeamMemberInput è il ruolo da assegnare a un team.
type SetTeamMemberInput struct {
	Role string // "member" o "maintainer"
}

// Service è il repository per organizzazioni e team.
type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// New crea un Service. now nil usa time.Now.
func New(pool *pgxpool.Pool, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{pool: pool, now: now}
}

// ---- Organizations ----

// Create crea un'organizzazione e aggiunge userID come owner nella stessa
// transazione. Ritorna ErrAlreadyExists se il nome esiste già.
type AlreadyExistsError struct{ Field string }

// ReservedNameError: il nome è riservato (R1, pkg/names): 400.
type ReservedNameError struct{ Name string }

func (e *ReservedNameError) Error() string { return "il nome " + e.Name + " è riservato" }

func (e *AlreadyExistsError) Error() string { return e.Field + " già in uso" }

func (s *Service) Create(ctx context.Context, in CreateOrgInput, userID uuid.UUID) (Organization, error) {
	if err := validateName(in.Name); err != nil {
		return Organization{}, err
	}
	if names.IsReservedOwnerName(in.Name) {
		return Organization{}, &ReservedNameError{Name: in.Name}
	}
	if len(in.DisplayName) > 128 {
		return Organization{}, &ValidationError{Fields: map[string]string{"displayName": "al massimo 128 caratteri"}}
	}
	if len(in.Description) > 1024 {
		return Organization{}, &ValidationError{Fields: map[string]string{"description": "al massimo 1024 caratteri"}}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Organization{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	now := s.now()
	orgID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO identity.organizations (id, name, display_name, description, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5)`,
		orgID, in.Name, in.DisplayName, in.Description, now)
	if err != nil {
		return Organization{}, mapUnique(err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO identity.org_members (org_id, user_id, role, created_at)
		VALUES ($1, $2, 'owner', $3)`,
		orgID, userID, now)
	if err != nil {
		return Organization{}, err
	}

	if err = tx.Commit(ctx); err != nil {
		return Organization{}, err
	}

	return Organization{ID: orgID, Name: in.Name, DisplayName: in.DisplayName, Description: in.Description, CreatedAt: now}, nil
}

// GetByID legge un'organizzazione per ID.
func (s *Service) GetByID(ctx context.Context, id uuid.UUID) (Organization, error) {
	var org Organization
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, display_name, description, created_at
		FROM identity.organizations WHERE id = $1`, id).
		Scan(&org.ID, &org.Name, &org.DisplayName, &org.Description, &org.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrNotFound
	}
	if err != nil {
		return Organization{}, fmt.Errorf("lettura organizzazione non riuscita: %w", err)
	}
	return org, nil
}

// GetByName legge un'organizzazione per nome.
func (s *Service) GetByName(ctx context.Context, name string) (Organization, error) {
	var org Organization
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, display_name, description, created_at
		FROM identity.organizations WHERE name = $1`, name).
		Scan(&org.ID, &org.Name, &org.DisplayName, &org.Description, &org.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrNotFound
	}
	if err != nil {
		return Organization{}, fmt.Errorf("lettura organizzazione non riuscita: %w", err)
	}
	return org, nil
}

// Update modifica un'organizzazione.
func (s *Service) Update(ctx context.Context, orgID uuid.UUID, in UpdateOrgInput) (Organization, error) {
	if in.DisplayName != nil && len(*in.DisplayName) > 128 {
		return Organization{}, &ValidationError{Fields: map[string]string{"displayName": "al massimo 128 caratteri"}}
	}
	if in.Description != nil && len(*in.Description) > 1024 {
		return Organization{}, &ValidationError{Fields: map[string]string{"description": "al massimo 1024 caratteri"}}
	}

	org, err := s.GetByID(ctx, orgID)
	if err != nil {
		return Organization{}, err
	}

	display := org.DisplayName
	if in.DisplayName != nil {
		display = *in.DisplayName
	}
	desc := org.Description
	if in.Description != nil {
		desc = *in.Description
	}

	now := s.now()
	_, err = s.pool.Exec(ctx, `
		UPDATE identity.organizations SET display_name=$1, description=$2, updated_at=$3
		WHERE id=$4 RETURNING id, name, display_name, description, created_at`,
		display, desc, now, orgID)
	if err != nil {
		return Organization{}, err
	}

	return Organization{ID: orgID, Name: org.Name, DisplayName: display, Description: desc, CreatedAt: org.CreatedAt}, nil
}

// Delete elimina un'organizzazione (a cascata team e membership via FK).
func (s *Service) Delete(ctx context.Context, orgID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM identity.organizations WHERE id = $1`, orgID)
	if err != nil {
		return fmt.Errorf("eliminazione organizzazione non riuscita: %w", err)
	}
	return nil
}

// List elenca le organizzazioni di cui userID è membro, con pagina.
func (s *Service) List(ctx context.Context, userID uuid.UUID, page, perPage int) ([]Organization, int, error) {
	offset := (page - 1) * perPage

	rows, err := s.pool.Query(ctx, `
		SELECT o.id, o.name, o.display_name, o.description, o.created_at
		FROM identity.organizations o
		JOIN identity.org_members m ON m.org_id = o.id AND m.user_id = $1
		ORDER BY o.name
		LIMIT $2 OFFSET $3`, userID, perPage, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("elenco organizzazioni non riuscito: %w", err)
	}
	defer rows.Close()

	var orgs []Organization
	for rows.Next() {
		var o Organization
		if err := rows.Scan(&o.ID, &o.Name, &o.DisplayName, &o.Description, &o.CreatedAt); err != nil {
			return nil, 0, err
		}
		orgs = append(orgs, o)
	}

	var total int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM identity.org_members WHERE user_id = $1`, userID).Scan(&total); err != nil {
		return nil, 0, err
	}

	return orgs, total, nil
}

// ListAll elenca tutte le organizzazioni (per admin di sistema).
func (s *Service) ListAll(ctx context.Context, page, perPage int) ([]Organization, int, error) {
	offset := (page - 1) * perPage

	rows, err := s.pool.Query(ctx, `
		SELECT id, name, display_name, description, created_at
		FROM identity.organizations
		ORDER BY name
		LIMIT $1 OFFSET $2`, perPage, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("elenco organizzazioni non riuscito: %w", err)
	}
	defer rows.Close()

	var orgs []Organization
	for rows.Next() {
		var o Organization
		if err := rows.Scan(&o.ID, &o.Name, &o.DisplayName, &o.Description, &o.CreatedAt); err != nil {
			return nil, 0, err
		}
		orgs = append(orgs, o)
	}

	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM identity.organizations`).Scan(&total); err != nil {
		return nil, 0, err
	}

	return orgs, total, nil
}

// ---- helper ----

var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,37}[a-z0-9])?$`)

// validateName verifica che il nome sia conforme allo schema (Name).
func validateName(name string) error {
	if len(name) < 1 || len(name) > 39 || !nameRe.MatchString(name) {
		return &ValidationError{Fields: map[string]string{"name": "caratteri minuscoli, cifre e trattini, 1-39, inizia e finisce con alfanumerico"}}
	}
	return nil
}

// join unisce una slice di stringhe con un separatore.
func join(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	r := parts[0]
	for _, p := range parts[1:] {
		r += sep + p
	}
	return r
}

// mapUnique traduce un errore di UNIQUE violation PostgreSQL in AlreadyExistsError.
func mapUnique(err error) error {
	if err == nil {
		return nil
	}
	// PostgreSQL errore 23505 = unique_violation
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return &AlreadyExistsError{Field: "nome organizzazione"}
	}
	return fmt.Errorf("errore univocita non riuscita: %w", err)
}

// validateRoleOrg verifica che un ruolo di org sia valido.
func validateRoleOrg(role string) error {
	switch role {
	case "owner", "member":
		return nil
	default:
		return &ValidationError{Fields: map[string]string{"role": "deve essere owner o member"}}
	}
}

// validateRoleTeam verifica che un ruolo di team sia valido.
func validateRoleTeam(role string) error {
	switch role {
	case "member", "maintainer":
		return nil
	default:
		return &ValidationError{Fields: map[string]string{"role": "deve essere member o maintainer"}}
	}
}

// ---- OrgMembers ----

// SetOrgMember aggiunge un utente all'organizzazione o ne cambia il ruolo.
// L'utente deve essere gia' membro di un team se viene declassato a owner
// o viceversa; in pratica, il caller deve verificare l'autorizzazione (403).
func (s *Service) SetOrgMember(ctx context.Context, orgID, userID uuid.UUID, in SetOrgMemberInput) (OrgMember, error) {
	if err := validateRoleOrg(in.Role); err != nil {
		return OrgMember{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OrgMember{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Se stiamo declassando un owner, verifichiamo che non sia l'ultimo.
	if in.Role != "owner" {
		isOwner, owners, err := lockOwners(ctx, tx, orgID, userID)
		if err != nil {
			return OrgMember{}, err
		}
		if isOwner && owners == 1 {
			return OrgMember{}, ErrLastOwner
		}
	}

	var createdAt time.Time
	err = tx.QueryRow(ctx, `
		INSERT INTO identity.org_members (org_id, user_id, role, created_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (org_id, user_id) DO UPDATE
			SET role = EXCLUDED.role
		RETURNING created_at`,
		orgID, userID, in.Role).Scan(&createdAt)
	if err != nil {
		return OrgMember{}, fmt.Errorf("impostazione membro organizzazione non riuscita: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return OrgMember{}, err
	}
	return OrgMember{UserID: userID, Role: in.Role, CreatedAt: createdAt}, nil
}

// RemoveOrgMember rimuove un membro dall'organizzazione.
// Non si rimuove l'ultimo owner.
func (s *Service) RemoveOrgMember(ctx context.Context, orgID, userID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	isOwner, owners, err := lockOwners(ctx, tx, orgID, userID)
	if err != nil {
		return err
	}
	if isOwner && owners == 1 {
		return ErrLastOwner
	}

	_, err = tx.Exec(ctx, `
		DELETE FROM identity.org_members WHERE org_id = $1 AND user_id = $2`,
		orgID, userID)
	if err != nil {
		return fmt.Errorf("rimozione membro organizzazione non riuscita: %w", err)
	}
	return tx.Commit(ctx)
}

// lockOwners blocca (FOR UPDATE) tutte le righe degli owner, compreso il
// bersaglio, e li conta in Go: FOR UPDATE non e' ammesso con gli aggregati.
// Bloccare tutti gli owner serializza le modifiche concorrenti.
func lockOwners(ctx context.Context, tx pgx.Tx, orgID, target uuid.UUID) (targetIsOwner bool, owners int, err error) {
	rows, err := tx.Query(ctx, `
		SELECT user_id FROM identity.org_members
		WHERE org_id = $1 AND role = 'owner'
		ORDER BY user_id
		FOR UPDATE`, orgID)
	if err != nil {
		return false, 0, fmt.Errorf("verifica owner non riuscita: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var uid uuid.UUID
		if err := rows.Scan(&uid); err != nil {
			return false, 0, err
		}
		owners++
		if uid == target {
			targetIsOwner = true
		}
	}
	return targetIsOwner, owners, rows.Err()
}

// Role restituisce il ruolo di userID in orgID; ErrNotOrgMember se non e' membro.
func (s *Service) Role(ctx context.Context, orgID, userID uuid.UUID) (string, error) {
	var role string
	err := s.pool.QueryRow(ctx, `
		SELECT role FROM identity.org_members
		WHERE org_id = $1 AND user_id = $2`, orgID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotOrgMember
	}
	if err != nil {
		return "", fmt.Errorf("lettura ruolo non riuscita: %w", err)
	}
	return role, nil
}

// ListOrgMembers elenca i membri di un'organizzazione.
func (s *Service) ListOrgMembers(ctx context.Context, orgID uuid.UUID, page, perPage int) ([]OrgMember, int, error) {
	offset := (page - 1) * perPage

	rows, err := s.pool.Query(ctx, `
		SELECT user_id, role, created_at
		FROM identity.org_members WHERE org_id = $1
		ORDER BY user_id
		LIMIT $2 OFFSET $3`, orgID, perPage, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("elenco membri organizzazione non riuscito: %w", err)
	}
	defer rows.Close()

	var members []OrgMember
	for rows.Next() {
		var m OrgMember
		if err := rows.Scan(&m.UserID, &m.Role, &m.CreatedAt); err != nil {
			return nil, 0, err
		}
		members = append(members, m)
	}

	var total int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM identity.org_members WHERE org_id = $1`, orgID).Scan(&total); err != nil {
		return nil, 0, err
	}

	return members, total, nil
}

// ---- Teams ----

// CreateTeam crea un team dentro un'organizzazione.
func (s *Service) CreateTeam(ctx context.Context, orgID uuid.UUID, in CreateTeamInput) (Team, error) {
	if err := validateName(in.Name); err != nil {
		return Team{}, err
	}
	if len(in.Description) > 1024 {
		return Team{}, &ValidationError{Fields: map[string]string{"description": "al massimo 1024 caratteri"}}
	}

	teamID := uuid.New()
	now := time.Now()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO identity.teams (id, org_id, name, description, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5)`,
		teamID, orgID, in.Name, in.Description, now)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Team{}, &AlreadyExistsError{Field: "nome team"}
		}
		return Team{}, fmt.Errorf("creazione team non riuscita: %w", err)
	}

	return Team{ID: teamID, OrgID: orgID, Name: in.Name, Description: in.Description, CreatedAt: now}, nil
}

// GetTeam legge un team per ID.
func (s *Service) GetTeam(ctx context.Context, orgID, teamID uuid.UUID) (Team, error) {
	var t Team
	err := s.pool.QueryRow(ctx, `
		SELECT id, org_id, name, description, created_at
		FROM identity.teams WHERE id = $1 AND org_id = $2`,
		teamID, orgID).Scan(&t.ID, &t.OrgID, &t.Name, &t.Description, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Team{}, ErrNotFound
	}
	if err != nil {
		return Team{}, fmt.Errorf("lettura team non riuscita: %w", err)
	}
	return t, nil
}

// UpdateTeam modifica un team.
func (s *Service) UpdateTeam(ctx context.Context, orgID, teamID uuid.UUID, in UpdateTeamInput) (Team, error) {
	team, err := s.GetTeam(ctx, orgID, teamID)
	if err != nil {
		return Team{}, err
	}

	name := team.Name
	if in.Name != nil {
		if err := validateName(*in.Name); err != nil {
			return Team{}, err
		}
		name = *in.Name
	}
	desc := team.Description
	if in.Description != nil {
		if len(*in.Description) > 1024 {
			return Team{}, &ValidationError{Fields: map[string]string{"description": "al massimo 1024 caratteri"}}
		}
		desc = *in.Description
	}

	now := time.Now()
	_, err = s.pool.Exec(ctx, `
		UPDATE identity.teams SET name=$1, description=$2, updated_at=$3
		WHERE id=$4 AND org_id=$5 RETURNING id, org_id, name, description, created_at`,
		name, desc, now, teamID, orgID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Team{}, &AlreadyExistsError{Field: "nome team"}
		}
		return Team{}, fmt.Errorf("aggiornamento team non riuscito: %w", err)
	}

	return Team{ID: teamID, OrgID: orgID, Name: name, Description: desc, CreatedAt: team.CreatedAt}, nil
}

// DeleteTeam elimina un team.
func (s *Service) DeleteTeam(ctx context.Context, orgID, teamID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM identity.teams WHERE id = $1 AND org_id = $2`, teamID, orgID)
	if err != nil {
		return fmt.Errorf("eliminazione team non riuscita: %w", err)
	}
	return nil
}

// ListTeams elenca i team di un'organizzazione.
func (s *Service) ListTeams(ctx context.Context, orgID uuid.UUID, page, perPage int) ([]Team, int, error) {
	offset := (page - 1) * perPage

	rows, err := s.pool.Query(ctx, `
		SELECT id, org_id, name, description, created_at
		FROM identity.teams WHERE org_id = $1
		ORDER BY name
		LIMIT $2 OFFSET $3`, orgID, perPage, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("elenco team non riuscito: %w", err)
	}
	defer rows.Close()

	var teams []Team
	for rows.Next() {
		var t Team
		if err := rows.Scan(&t.ID, &t.OrgID, &t.Name, &t.Description, &t.CreatedAt); err != nil {
			return nil, 0, err
		}
		teams = append(teams, t)
	}

	var total int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM identity.teams WHERE org_id = $1`, orgID).Scan(&total); err != nil {
		return nil, 0, err
	}

	return teams, total, nil
}

// ---- TeamMembers ----

// SetTeamMember aggiunge un membro al team o ne cambia il ruolo.
// L'utente deve essere gia' membro dell'organizzazione.
func (s *Service) SetTeamMember(ctx context.Context, orgID, teamID, userID uuid.UUID, in SetTeamMemberInput) (TeamMember, error) {
	if err := validateRoleTeam(in.Role); err != nil {
		return TeamMember{}, err
	}

	// Verifica che l'utente sia membro dell'organizzazione.
	var memberCount int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM identity.org_members WHERE org_id = $1 AND user_id = $2`,
		orgID, userID).Scan(&memberCount)
	if err != nil {
		return TeamMember{}, fmt.Errorf("verifica membro organizzazione non riuscita: %w", err)
	}
	if memberCount == 0 {
		return TeamMember{}, ErrNotOrgMember
	}

	// Verifica che il team esista e appartenga all'org.
	var tid uuid.UUID
	err = s.pool.QueryRow(ctx, `SELECT id FROM identity.teams WHERE id = $1 AND org_id = $2`,
		teamID, orgID).Scan(&tid)
	if errors.Is(err, pgx.ErrNoRows) {
		return TeamMember{}, ErrNotFound
	}
	if err != nil {
		return TeamMember{}, fmt.Errorf("verifica team non riuscita: %w", err)
	}

	now := time.Now()
	_, err = s.pool.Exec(ctx, `
		INSERT INTO identity.team_members (team_id, org_id, user_id, role, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (team_id, user_id) DO UPDATE
			SET role = EXCLUDED.role
		RETURNING user_id, role, created_at`,
		teamID, orgID, userID, in.Role, now)
	if err != nil {
		return TeamMember{}, fmt.Errorf("impostazione membro team non riuscita: %w", err)
	}

	return TeamMember{UserID: userID, Role: in.Role, CreatedAt: now}, nil
}

// RemoveTeamMember rimuove un membro dal team.
func (s *Service) RemoveTeamMember(ctx context.Context, orgID, teamID, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM identity.team_members WHERE team_id = $1 AND org_id = $2 AND user_id = $3`,
		teamID, orgID, userID)
	if err != nil {
		return fmt.Errorf("rimozione membro team non riuscita: %w", err)
	}
	return nil
}

// ListTeamMembers elenca i membri di un team.
func (s *Service) ListTeamMembers(ctx context.Context, orgID, teamID uuid.UUID, page, perPage int) ([]TeamMember, int, error) {
	offset := (page - 1) * perPage

	rows, err := s.pool.Query(ctx, `
		SELECT user_id, role, created_at
		FROM identity.team_members WHERE team_id = $1 AND org_id = $2
		ORDER BY user_id
		LIMIT $3 OFFSET $4`, teamID, orgID, perPage, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("elenco membri team non riuscito: %w", err)
	}
	defer rows.Close()

	var members []TeamMember
	for rows.Next() {
		var m TeamMember
		if err := rows.Scan(&m.UserID, &m.Role, &m.CreatedAt); err != nil {
			return nil, 0, err
		}
		members = append(members, m)
	}

	var total int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM identity.team_members WHERE team_id = $1 AND org_id = $2`,
		teamID, orgID).Scan(&total); err != nil {
		return nil, 0, err
	}

	return members, total, nil
}

// IsOrgMember verifica se userID e' membro di orgID.
func (s *Service) IsOrgMember(ctx context.Context, orgID, userID uuid.UUID) (bool, error) {
	var count int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM identity.org_members WHERE org_id = $1 AND user_id = $2`,
		orgID, userID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("verifica membro organizzazione non riuscita: %w", err)
	}
	return count > 0, nil
}

// HasOwner verifica se un'organizzazione ha almeno un owner.
func (s *Service) HasOwner(ctx context.Context, orgID uuid.UUID) (bool, error) {
	var count int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM identity.org_members WHERE org_id = $1 AND role = 'owner'`,
		orgID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("verifica owner non riuscita: %w", err)
	}
	return count > 0, nil
}

// ListOwners elenca gli owner di un'organizzazione.
func (s *Service) ListOwners(ctx context.Context, orgID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT user_id FROM identity.org_members WHERE org_id = $1 AND role = 'owner'`,
		orgID)
	if err != nil {
		return nil, fmt.Errorf("elenco owner non riuscito: %w", err)
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// GetTeamByName legge un team per nome nell'organizzazione.
func (s *Service) GetTeamByName(ctx context.Context, orgID uuid.UUID, teamName string) (Team, error) {
	var t Team
	err := s.pool.QueryRow(ctx, `
		SELECT id, org_id, name, description, created_at
		FROM identity.teams WHERE org_id = $1 AND name = $2`,
		orgID, teamName).Scan(&t.ID, &t.OrgID, &t.Name, &t.Description, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Team{}, ErrNotFound
	}
	if err != nil {
		return Team{}, fmt.Errorf("lettura team non riuscita: %w", err)
	}
	return t, nil
}

// RemoveTeamMemberByOrg elimina un membro da un team tramite org_id.
func (s *Service) RemoveTeamMemberByOrg(ctx context.Context, orgID, teamID, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM identity.team_members WHERE team_id = $1 AND org_id = $2 AND user_id = $3`,
		teamID, orgID, userID)
	if err != nil {
		return fmt.Errorf("rimozione membro team non riuscita: %w", err)
	}
	return nil
}
