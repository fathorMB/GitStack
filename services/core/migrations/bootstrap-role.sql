-- Bootstrap manuale, non eseguito da core: lo esegue un operatore con un
-- ruolo Postgres superuser (o comunque con CREATEROLE) una sola volta per
-- installazione, prima del primo avvio di core, oppure lo automatizza il
-- deploy k3s (M-01/T-08, GIT-8), non il servizio applicativo.
--
-- Scopo (D6 [c_4df04d65b3ac4910], nota del CTO su GIT-5): un ruolo DB
-- dedicato a core, proprietario del solo schema "core", senza permessi su
-- schema o tabelle di altri servizi (identity, git, ...) che condividono lo
-- stesso database Postgres.
--
-- Il ruolo è OWNER dello schema "core": può quindi creare/modificare le
-- proprie tabelle (le migrazioni applicate da core stesso, vedi
-- internal/migrate) senza bisogno di CREATE su tutto il database, e non ha
-- alcun permesso implicito sugli altri schema.
--
-- Sostituire ':core_password' con un segreto vero (es. da un secret k8s),
-- non committare mai la password reale in chiaro.

CREATE ROLE core_app WITH LOGIN PASSWORD :'core_password';

CREATE SCHEMA IF NOT EXISTS core AUTHORIZATION core_app;

-- Nessun permesso di default su "public" o su schema di altri servizi: il
-- ruolo core_app non riceve alcun GRANT fuori da "core".
REVOKE ALL ON SCHEMA public FROM core_app;

-- Con lo schema di proprietà, core_app può già creare tabelle/indici al suo
-- interno (le migrazioni di internal/migrate/sql lo fanno con
-- CREATE ... IF NOT EXISTS); qui rendiamo espliciti anche i privilegi sugli
-- oggetti già esistenti, per chi esegue questo script su un database non
-- vuoto.
GRANT ALL PRIVILEGES ON SCHEMA core TO core_app;
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA core TO core_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA core GRANT ALL PRIVILEGES ON TABLES TO core_app;
