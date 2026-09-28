-- Rollback di 0002, in ordine inverso di dipendenza.
DROP TABLE IF EXISTS identity.resource_grants;
DROP TABLE IF EXISTS identity.team_members;
DROP TABLE IF EXISTS identity.teams;
DROP TABLE IF EXISTS identity.org_members;
DROP TABLE IF EXISTS identity.organizations;
