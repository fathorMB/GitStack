-- v2-a (GIT-276): CI3 e coda di CI7. Modello dati di esecuzioni (ci_runs),
-- job (ci_jobs) e passi (ci_steps) dell'ADR 0001: scheduler e coda dentro core,
-- coda su Postgres con FOR UPDATE SKIP LOCKED. Le tabelle sono pensate anche per
-- gli item successivi (token del job, log, runner): vedi i commenti per colonna.

-- Stato CI di un repo: interruttore per i repo in mirror in pull (spenta di
-- default) e data dell'ultimo push (per sospendere gli schedule dopo 60 giorni).
CREATE TABLE core.ci_repo_state (
    repo_id UUID PRIMARY KEY REFERENCES core.repositories (resource_id) ON DELETE CASCADE,
    run_on_pull_mirror BOOLEAN NOT NULL DEFAULT false,
    last_push_at TIMESTAMPTZ NULL
);

-- Un'esecuzione di un workflow. owner_* e' copiato dal repo: la coda equa conta i
-- job in corso per owner. group_key e' il «branch o PR» che un nuovo push
-- supera: push:<ref>, pull_request:<numero>; per dispatch e schedule e' unico.
CREATE TABLE core.ci_runs (
    id UUID PRIMARY KEY,
    repo_id UUID NOT NULL REFERENCES core.repositories (resource_id) ON DELETE CASCADE,
    owner_type TEXT NOT NULL CHECK (owner_type IN ('user', 'organization')),
    owner_id UUID NOT NULL,
    workflow_path TEXT NOT NULL CHECK (char_length(workflow_path) BETWEEN 1 AND 512),
    workflow_name TEXT NOT NULL DEFAULT '',
    event TEXT NOT NULL CHECK (event IN ('push', 'pull_request', 'workflow_dispatch', 'schedule')),
    ref TEXT NOT NULL,
    sha TEXT NOT NULL CHECK (sha ~ '^[0-9a-f]{40}$'),
    pull_number BIGINT NULL,
    group_key TEXT NOT NULL,
    -- NULL per schedule (nessun attore).
    actor_id UUID NULL,
    inputs JSONB NOT NULL DEFAULT '{}'::jsonb,
    status TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'in_progress', 'completed', 'cancelled')),
    conclusion TEXT NULL CHECK (conclusion IN ('success', 'failure', 'cancelled', 'skipped', 'timed_out')),
    -- Istante programmato di uno schedule: la stessa scadenza non parte due volte.
    schedule_slot TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ NULL,
    completed_at TIMESTAMPTZ NULL,
    CONSTRAINT ci_runs_completed_has_conclusion CHECK ((status IN ('completed', 'cancelled')) = (completed_at IS NOT NULL)),
    CONSTRAINT ci_runs_schedule_has_slot CHECK ((event = 'schedule') = (schedule_slot IS NOT NULL))
);
CREATE INDEX ci_runs_repo_idx ON core.ci_runs (repo_id, created_at DESC, id);
CREATE INDEX ci_runs_group_queued_idx ON core.ci_runs (repo_id, workflow_path, group_key) WHERE status = 'queued';
CREATE UNIQUE INDEX ci_runs_schedule_slot_key ON core.ci_runs (repo_id, workflow_path, schedule_slot) WHERE event = 'schedule';

-- Un job = un pod usa e getta (ADR 0001). waiting: aspetta i job di `needs`;
-- queued: in coda; token_principal_id e' l'identita' del token effimero del job
-- (riempita dall'item dei token): serve a riconoscere un push fatto dal job.
CREATE TABLE core.ci_jobs (
    id UUID PRIMARY KEY,
    run_id UUID NOT NULL REFERENCES core.ci_runs (id) ON DELETE CASCADE,
    seq INTEGER NOT NULL CHECK (seq >= 0),
    job_key TEXT NOT NULL,
    name TEXT NOT NULL,
    needs TEXT[] NOT NULL DEFAULT '{}',
    runner_type TEXT NOT NULL DEFAULT 'internal_pod' CHECK (runner_type IN ('internal_pod', 'external')),
    runner_name TEXT NULL,
    permissions JSONB NOT NULL DEFAULT '{"contents":"read","packages":"read","actions":"read"}'::jsonb,
    token_principal_id UUID NULL,
    status TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('waiting', 'queued', 'in_progress', 'completed', 'cancelled')),
    conclusion TEXT NULL CHECK (conclusion IN ('success', 'failure', 'cancelled', 'skipped', 'timed_out')),
    queued_at TIMESTAMPTZ NULL,
    started_at TIMESTAMPTZ NULL,
    completed_at TIMESTAMPTZ NULL,
    CONSTRAINT ci_jobs_run_key UNIQUE (run_id, job_key),
    CONSTRAINT ci_jobs_run_seq UNIQUE (run_id, seq)
);
CREATE INDEX ci_jobs_queue_idx ON core.ci_jobs (queued_at, seq) WHERE status = 'queued';
CREATE INDEX ci_jobs_running_idx ON core.ci_jobs (run_id) WHERE status = 'in_progress';
CREATE INDEX ci_jobs_principal_idx ON core.ci_jobs (token_principal_id) WHERE token_principal_id IS NOT NULL;

CREATE TABLE core.ci_steps (
    id UUID PRIMARY KEY,
    job_id UUID NOT NULL REFERENCES core.ci_jobs (id) ON DELETE CASCADE,
    number INTEGER NOT NULL CHECK (number >= 1),
    name TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'in_progress', 'completed', 'cancelled')),
    conclusion TEXT NULL CHECK (conclusion IN ('success', 'failure', 'cancelled', 'skipped', 'timed_out')),
    started_at TIMESTAMPTZ NULL,
    completed_at TIMESTAMPTZ NULL,
    CONSTRAINT ci_steps_job_number UNIQUE (job_id, number)
);

-- Orari (on.schedule) dei workflow del branch principale, aggiornati a ogni push
-- su di esso. suspended_at: sospeso perche' il repo non riceve push da 60 giorni;
-- un push nuovo lo riattiva.
CREATE TABLE core.ci_schedules (
    id UUID PRIMARY KEY,
    repo_id UUID NOT NULL REFERENCES core.repositories (resource_id) ON DELETE CASCADE,
    workflow_path TEXT NOT NULL,
    workflow_name TEXT NOT NULL DEFAULT '',
    cron TEXT NOT NULL CHECK (char_length(cron) BETWEEN 1 AND 128),
    ref TEXT NOT NULL,
    sha TEXT NOT NULL CHECK (sha ~ '^[0-9a-f]{40}$'),
    next_run_at TIMESTAMPTZ NOT NULL,
    suspended_at TIMESTAMPTZ NULL,
    CONSTRAINT ci_schedules_key UNIQUE (repo_id, workflow_path, cron)
);
CREATE INDEX ci_schedules_due_idx ON core.ci_schedules (next_run_at) WHERE suspended_at IS NULL;
