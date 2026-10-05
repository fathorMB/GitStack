-- M-03/C (GIT-65): spazio di nomi unico utenti/organizzazioni (R1) e
-- attributi delle risorse (owner e visibilita', P1/P3/P6).

-- 1. Prima di imporre l'unicita' comune si verifica che nel database non ci
--    siano gia' collisioni fra username e nomi di organizzazione: se ci
--    sono, la migrazione si ferma con un messaggio che le elenca, e va
--    risolta a mano (rinominando uno dei due) prima di riprovare.
DO $$
DECLARE
    clash text;
BEGIN
    SELECT string_agg(u.username, ', ' ORDER BY u.username) INTO clash
    FROM identity.users u
    JOIN identity.organizations o ON o.name = u.username;
    IF clash IS NOT NULL THEN
        RAISE EXCEPTION 'migrazione 0003 interrotta: esistono nomi usati sia da un utente sia da un''organizzazione (%): rinominare uno dei due e rieseguire', clash
            USING ERRCODE = 'unique_violation';
    END IF;
END $$;

-- 2. Registro dei nomi di owner: una riga per utente e per organizzazione,
--    mantenuta da trigger. L'unicita' di "name" e' quella comune.
CREATE TABLE identity.owner_names (
    name       TEXT PRIMARY KEY,
    owner_type TEXT NOT NULL CHECK (owner_type IN ('user', 'organization')),
    owner_id   UUID NOT NULL,
    CONSTRAINT owner_names_owner_key UNIQUE (owner_type, owner_id)
);

INSERT INTO identity.owner_names (name, owner_type, owner_id)
SELECT username, 'user', id FROM identity.users;
INSERT INTO identity.owner_names (name, owner_type, owner_id)
SELECT name, 'organization', id FROM identity.organizations;

CREATE OR REPLACE FUNCTION identity.sync_owner_name_user() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        INSERT INTO identity.owner_names (name, owner_type, owner_id) VALUES (NEW.username, 'user', NEW.id);
    ELSIF TG_OP = 'UPDATE' THEN
        IF NEW.username IS DISTINCT FROM OLD.username THEN
            UPDATE identity.owner_names SET name = NEW.username WHERE owner_type = 'user' AND owner_id = OLD.id;
        END IF;
    ELSE
        DELETE FROM identity.owner_names WHERE owner_type = 'user' AND owner_id = OLD.id;
        RETURN OLD;
    END IF;
    RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION identity.sync_owner_name_org() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        INSERT INTO identity.owner_names (name, owner_type, owner_id) VALUES (NEW.name, 'organization', NEW.id);
    ELSIF TG_OP = 'UPDATE' THEN
        IF NEW.name IS DISTINCT FROM OLD.name THEN
            UPDATE identity.owner_names SET name = NEW.name WHERE owner_type = 'organization' AND owner_id = OLD.id;
        END IF;
    ELSE
        DELETE FROM identity.owner_names WHERE owner_type = 'organization' AND owner_id = OLD.id;
        RETURN OLD;
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER users_owner_name AFTER INSERT OR UPDATE OF username OR DELETE ON identity.users
    FOR EACH ROW EXECUTE FUNCTION identity.sync_owner_name_user();
CREATE TRIGGER organizations_owner_name AFTER INSERT OR UPDATE OF name OR DELETE ON identity.organizations
    FOR EACH ROW EXECUTE FUNCTION identity.sync_owner_name_org();

-- 3. Attributi delle risorse impostati da core (PUT
--    /internal/resources/{id}/attributes). resource_id punta a core.resources
--    senza FK (D6). Una risorsa senza riga si comporta come prima di M-03.
--    owner_id punta a users o organizations a seconda di owner_type.
CREATE TABLE identity.resource_attributes (
    resource_id UUID PRIMARY KEY,
    owner_type  TEXT NOT NULL CHECK (owner_type IN ('user', 'organization')),
    owner_id    UUID NOT NULL,
    visibility  TEXT NOT NULL DEFAULT 'private' CHECK (visibility IN ('private', 'internal')),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX resource_attributes_owner_idx ON identity.resource_attributes (owner_type, owner_id);
CREATE INDEX resource_attributes_internal_idx ON identity.resource_attributes (resource_id) WHERE visibility = 'internal';
