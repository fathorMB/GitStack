-- Preparazione di un PostgreSQL esterno per GitStack (N8 [c_691e437fe2febab6]).
--
-- Lo esegue UNA VOLTA il DBA, con un ruolo che può creare ruoli e database
-- (di solito `postgres`), prima di lanciare `install.sh --postgres-url ...`.
-- Idempotente: si può rieseguire (riallinea la password dell'utente).
--
--   export GITSTACK_DB_PASSWORD='<password scelta per l'utente di GitStack>'
--   psql "postgres://postgres@db.example.com:5432/postgres?sslmode=require" \
--        -v ON_ERROR_STOP=1 -f deploy/postgres-prepare.sql
--
-- Variabili d'ambiente lette da psql (\getenv, servono psql 15 o più
-- recente; la password non finisce mai nella riga di comando):
--   GITSTACK_DB_PASSWORD  obbligatoria
--   GITSTACK_DB_NAME      database dedicato (default: gitstack)
--   GITSTACK_DB_USER      utente di GitStack (default: gitstack)
--
-- Cosa crea:
--   * un utente LOGIN con CREATEROLE, NON superuser: GitStack vi crea un
--     ruolo applicativo e uno schema per servizio (identity_app e schema
--     identity; schema core), come con il Postgres interno;
--   * un database dedicato, di cui l'utente è proprietario: GitStack non
--     usa mai il database `postgres` né altri database;
--   * nessun REVOKE di CONNECT da PUBLIC: il ruolo applicativo che GitStack
--     crea per identity (identity_app) deve potersi connettere. Se la tua
--     politica lo vieta, concedi CONNECT a identity_app dopo l'installazione.
--
-- Cosa NON serve: superuser, estensioni, replica. PostgreSQL supportato: 15,
-- 16 e 17.
--
-- Dopo questo script, il preflight dell'installer (e `gitstack postgres
-- check`) controlla versione, connessione e permessi.

\getenv gs_password GITSTACK_DB_PASSWORD
\getenv gs_db GITSTACK_DB_NAME
\getenv gs_user GITSTACK_DB_USER

\if :{?gs_db}
\else
  \set gs_db gitstack
\endif
\if :{?gs_user}
\else
  \set gs_user gitstack
\endif
\if :{?gs_password}
\else
  \echo 'ERRORE: imposta GITSTACK_DB_PASSWORD (la password dell''utente di GitStack).'
  \quit 3
\endif
\if :{?gs_db}
\endif

SELECT current_setting('server_version_num')::int AS ver,
       current_setting('server_version_num')::int BETWEEN 150000 AND 179999 AS supported \gset
\if :supported
\else
  \echo 'ERRORE: PostgreSQL' :ver 'non supportato: servono le versioni 15, 16 o 17.'
  \quit 3
\endif

SELECT format('CREATE ROLE %I WITH LOGIN CREATEROLE PASSWORD %L', :'gs_user', :'gs_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'gs_user')
\gexec

SELECT format('ALTER ROLE %I WITH LOGIN CREATEROLE PASSWORD %L', :'gs_user', :'gs_password')
WHERE EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'gs_user')
\gexec

-- Il database lo crea il DBA con l'utente come proprietario. Se l'utente
-- non è membro di un ruolo gestito (es. servizi cloud), il DBA lo crea a mano.
SELECT format('CREATE DATABASE %I OWNER %I', :'gs_db', :'gs_user')
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = :'gs_db')
\gexec

SELECT format('GRANT ALL ON DATABASE %I TO %I', :'gs_db', :'gs_user') \gexec

\echo 'Pronto: database' :gs_db 'e utente' :gs_user '.'
\echo 'Installa con: install.sh --postgres-url postgres://' :gs_user '@<host>:5432/' :gs_db '?sslmode=require --postgres-password-file <file>'
