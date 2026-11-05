-- v3-d (GIT-557, ADR 0003, DB23): impostazioni d'istanza dei database gestiti, cambiate dalla console Admin.
-- Una sola riga. NULL = nessun valore scelto dalla console: vale quello del chart (major, porte LAN) o il
-- predefinito (limiti per database, conservazione dei backup). L'interruttore `enabled` non sta qui: lo
-- decide il chart (databases.enabled), perche' accendere o spegnere toglie e dà permessi Kubernetes.
CREATE TABLE core.managed_db_settings (
    singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
    allowed_majors INTEGER[] NULL CHECK (allowed_majors IS NULL OR (cardinality(allowed_majors) >= 1 AND allowed_majors <@ ARRAY[16, 17])),
    max_size_bytes BIGINT NULL CHECK (max_size_bytes IS NULL OR max_size_bytes >= 1),
    max_cpu_millis INTEGER NULL CHECK (max_cpu_millis IS NULL OR max_cpu_millis >= 1),
    max_memory_bytes BIGINT NULL CHECK (max_memory_bytes IS NULL OR max_memory_bytes >= 1),
    lan_port_from INTEGER NULL CHECK (lan_port_from IS NULL OR lan_port_from BETWEEN 1024 AND 65535),
    lan_port_to INTEGER NULL CHECK (lan_port_to IS NULL OR lan_port_to BETWEEN 1024 AND 65535),
    keep_scheduled INTEGER NULL CHECK (keep_scheduled IS NULL OR keep_scheduled >= 1),
    keep_on_demand INTEGER NULL CHECK (keep_on_demand IS NULL OR keep_on_demand >= 1),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT managed_db_settings_lan_order CHECK ((lan_port_from IS NULL) = (lan_port_to IS NULL) AND (lan_port_from IS NULL OR lan_port_from <= lan_port_to))
);
