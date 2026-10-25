-- H11 (GIT-345): riserva globale e massimo per app, separati dalle quote owner.
CREATE TABLE core.app_capacity_settings (
    singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
    apps_cpu_millis INTEGER NULL CHECK (apps_cpu_millis >= 1),
    apps_memory_bytes BIGINT NULL CHECK (apps_memory_bytes >= 1),
    app_max_cpu_millis INTEGER NOT NULL DEFAULT 4000 CHECK (app_max_cpu_millis >= 1),
    app_max_memory_bytes BIGINT NOT NULL DEFAULT 8589934592 CHECK (app_max_memory_bytes >= 1),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((apps_cpu_millis IS NULL) = (apps_memory_bytes IS NULL))
);
