-- v3-a (GIT-385, H16): uscita delle app. Le destinazioni private (es. db.azienda.lan:5432) le apre
-- l'amministratore dell'installazione; ogni app puo' poi restringere la propria uscita a un elenco.
-- Le regole di fondo (loopback, cluster, nodo, porte di Kubernetes) stanno in pkg/egress e non si
-- aprono da qui: l'API le rifiuta e il controller le ricontrolla prima di scrivere la NetworkPolicy.
CREATE TABLE core.app_private_destinations (
    id UUID PRIMARY KEY,
    host TEXT NOT NULL CHECK (char_length(host) BETWEEN 1 AND 253),
    port INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    note TEXT NOT NULL DEFAULT '' CHECK (char_length(note) <= 200),
    created_by JSONB NULL,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (host, port)
);

-- Restrizione per app: nessuna riga = uscita come l'istanza (internet aperto piu' le destinazioni
-- private aperte); una riga = l'app raggiunge solo gli indirizzi elencati, oltre ai peer dello
-- stesso repo e al DNS. destinations e' un array JSON di {host, port}.
CREATE TABLE core.app_egress_restrictions (
    app_id UUID PRIMARY KEY REFERENCES core.apps (id) ON DELETE CASCADE,
    destinations JSONB NOT NULL CHECK (jsonb_typeof(destinations) = 'array'),
    updated_by JSONB NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
