-- GIT-551: il down toglie i database gestiti dal modello; i namespace gitstack-db-* nel cluster non
-- si toccano da qui (li elimina il controller con l'eliminazione, DB19).
DROP TABLE IF EXISTS core.managed_db_audit;
DROP TABLE IF EXISTS core.managed_db_links;
DROP TABLE IF EXISTS core.managed_dbs;
