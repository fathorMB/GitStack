-- M-03/E (GIT-67): nome dell'owner nei dettagli del repo. L'owner (utente o
-- organizzazione) vive in identity; core ne tiene una copia del nome, scritta
-- alla creazione, per risolvere /repos/{owner}/{repo} e comporre fullName e
-- indirizzi di clone negli elenchi senza una chiamata a identity per riga.
-- Rinomina e trasferimento sono fuori dalla v1 (R3). Prima di questa
-- migrazione le operazioni sui repo rispondevano 501: non esistono righe.
ALTER TABLE core.repositories ADD COLUMN owner_name TEXT NOT NULL DEFAULT '';
CREATE INDEX repositories_owner_name_idx ON core.repositories (owner_name, name);
