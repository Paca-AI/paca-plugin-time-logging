-- 0001_create_task_time_logs.sql
-- Creates the time log table in the plugin schema.
-- Run with search_path = plugin_data_com_paca_time_logging, public.

CREATE TABLE IF NOT EXISTS task_time_logs (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id       UUID        NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    member_id     UUID        NOT NULL REFERENCES project_members(id) ON DELETE CASCADE,
    spent_date    DATE        NOT NULL,
    minutes_spent INTEGER     NOT NULL CHECK (minutes_spent > 0),
    note          TEXT        NOT NULL DEFAULT '',
    created_by    UUID        REFERENCES project_members(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_task_time_logs_task_id
    ON task_time_logs (task_id, spent_date);

CREATE INDEX IF NOT EXISTS idx_task_time_logs_member_id
    ON task_time_logs (member_id, spent_date);
