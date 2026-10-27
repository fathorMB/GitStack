-- v3-b (GIT-357, H3, D19): ogni volume dell'app entra nel backup di default e si può escludere.
-- L'esclusione è una scelta sul dato, non configurazione dell'app: non crea versioni e il rollback
-- non la cambia.
ALTER TABLE core.app_volumes ADD COLUMN include_in_backup BOOLEAN NOT NULL DEFAULT TRUE;
