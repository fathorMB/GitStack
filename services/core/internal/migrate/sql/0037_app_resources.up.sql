-- H11 (GIT-345): la riserva globale e' separata dalle quote per owner.
-- Una sola riga conserva soltanto un override amministrativo; NULL lascia il
-- valore calcolato dal profilo hardware esposto da core.
CREATE TABLE core.app_capacity_settings (
    singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
    apps_cpu_millis INTEGER NULL CHECK (apps_cpu_millis >= 1),
    apps_memory_bytes BIGINT NULL CHECK (apps_memory_bytes >= 1),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((apps_cpu_millis IS NULL) = (apps_memory_bytes IS NULL))
);
