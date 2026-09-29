-- Bootstrap del ruolo e dello schema di identity. Idempotente: si può
-- rieseguire a ogni avvio del servizio. Lo esegue, con le credenziali
-- amministrative di Postgres, l'initContainer "bootstrap-db" del Deployment
-- di identity nel chart deploy/gitstack (templates/identity/deployment.yaml);
-- in un'installazione senza il chart lo esegue un operatore con un ruolo
-- superuser (o comunque con CREATEROLE), passando la password con
-- `psql -v identity_password=...`.
--
-- ATTENZIONE: questo file esiste in due copie identiche,
-- services/identity/migrations/bootstrap-role.sql e
-- deploy/gitstack/files/identity-bootstrap-role.sql (un chart Helm non può
-- leggere file fuori dalla propria cartella). Il job "chart" della CI
-- verifica che siano uguali: modificale insieme.
--
-- Scopo (D6 [c_4df04d65b3ac4910], nota del CTO su GIT-5): un ruolo DB
-- dedicato a identity, proprietario del solo schema "identity", senza permessi su
-- schema o tabelle di altri servizi (core, git, ...) che condividono lo
-- stesso database Postgres.
--
-- Il ruolo è OWNER dello schema "identity": può quindi creare/modificare le
-- proprie tabelle (le migrazioni applicate da identity stesso, vedi
-- internal/migrate) senza bisogno di CREATE su tutto il database, e non ha
-- alcun permesso implicito sugli altri schema.
--
-- La password arriva dalla variabile psql :identity_password (mai in chiaro
-- nel file). Il ruolo si crea solo se manca (controllo su pg_roles, non
-- "IF NOT EXISTS", che non esiste per CREATE ROLE); se esiste già, la
-- password viene riallineata a quella del Secret.

SELECT format('CREATE ROLE identity_app WITH LOGIN PASSWORD %L', :'identity_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'identity_app')
\gexec

SELECT format('ALTER ROLE identity_app WITH LOGIN PASSWORD %L', :'identity_password')
WHERE EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'identity_app')
\gexec

SELECT 'CREATE SCHEMA identity AUTHORIZATION identity_app'
WHERE NOT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = 'identity')
\gexec

-- Nessun permesso di default su "public" o su schema di altri servizi: il
-- ruolo identity_app non riceve alcun GRANT fuori da "identity".
--
-- "REVOKE ALL ON SCHEMA public FROM identity_app" da solo non basterebbe: i
-- privilegi su "public" arrivano dal ruolo implicito PUBLIC (concesso a
-- ogni ruolo, incluso identity_app), non da un GRANT diretto a identity_app: va
-- revocato da PUBLIC, non da identity_app. Questo è globale al database (tocca
-- ogni ruolo che vi si connette, non solo identity_app): è la linea di base
-- comunque raccomandata quando più servizi condividono lo stesso database
-- (nessuno crea oggetti in "public"), Postgres 15+ lo fa già di default per
-- i nuovi database; qui lo rendiamo esplicito per non dipendere dalla
-- versione del cluster.
REVOKE CREATE ON SCHEMA public FROM PUBLIC;

-- Con lo schema di proprietà, identity_app può già creare tabelle/indici al suo
-- interno; rendiamo espliciti anche i privilegi sugli oggetti già esistenti,
-- per chi esegue questo script su un database non vuoto.
GRANT ALL PRIVILEGES ON SCHEMA identity TO identity_app;
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA identity TO identity_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA identity GRANT ALL PRIVILEGES ON TABLES TO identity_app;
