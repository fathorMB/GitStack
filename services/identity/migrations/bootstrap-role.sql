-- Bootstrap manuale, non eseguito da identity: lo esegue un operatore con un
-- ruolo Postgres superuser (o comunque con CREATEROLE) una sola volta per
-- installazione, prima del primo avvio di identity, oppure lo automatizza il
-- deploy k3s (M-01/T-08, GIT-8), non il servizio applicativo.
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
-- Sostituire ':identity_password' con un segreto vero (es. da un secret k8s),
-- non committare mai la password reale in chiaro.

CREATE ROLE identity_app WITH LOGIN PASSWORD :'identity_password';

CREATE SCHEMA IF NOT EXISTS identity AUTHORIZATION identity_app;

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
-- interno (le migrazioni di internal/migrate/sql lo fanno con
-- CREATE ... IF NOT EXISTS); qui rendiamo espliciti anche i privilegi sugli
-- oggetti già esistenti, per chi esegue questo script su un database non
-- vuoto.
GRANT ALL PRIVILEGES ON SCHEMA identity TO identity_app;
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA identity TO identity_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA identity GRANT ALL PRIVILEGES ON TABLES TO identity_app;
