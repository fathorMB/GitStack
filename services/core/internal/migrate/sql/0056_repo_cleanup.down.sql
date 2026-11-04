-- GIT-530: i repo eliminati non hanno piu' cestino; la coda repo_cleanup si svuota da sola. Il down non riporta indietro i repo gia' cancellati.
DROP TABLE IF EXISTS core.repo_cleanup;
