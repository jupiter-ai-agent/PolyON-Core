-- Migration 001: audit_log table
CREATE TABLE IF NOT EXISTS audit_log (
    id          SERIAL PRIMARY KEY,
    timestamp   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    action      VARCHAR(50)  NOT NULL,
    object_type VARCHAR(30)  NOT NULL,
    object_name VARCHAR(255) NOT NULL,
    actor       VARCHAR(100) DEFAULT 'Administrator',
    details     TEXT         DEFAULT '',
    ip_address  VARCHAR(45)  DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_audit_timestamp   ON audit_log(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_audit_action      ON audit_log(action);
CREATE INDEX IF NOT EXISTS idx_audit_object_type ON audit_log(object_type);
