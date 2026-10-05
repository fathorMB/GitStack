-- Rollback di 0003.
DROP TABLE IF EXISTS identity.resource_attributes;
DROP TRIGGER IF EXISTS organizations_owner_name ON identity.organizations;
DROP TRIGGER IF EXISTS users_owner_name ON identity.users;
DROP FUNCTION IF EXISTS identity.sync_owner_name_org();
DROP FUNCTION IF EXISTS identity.sync_owner_name_user();
DROP TABLE IF EXISTS identity.owner_names;
