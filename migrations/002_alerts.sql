-- Migration 002: sentinel_alerts table
CREATE TABLE IF NOT EXISTS sentinel_alerts (
    id           SERIAL PRIMARY KEY,
    timestamp    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    level        VARCHAR(20)  NOT NULL,
    source       VARCHAR(50)  DEFAULT 'sentinel',
    service      VARCHAR(100) NOT NULL,
    message      TEXT         NOT NULL,
    details      JSONB,
    acknowledged BOOLEAN      DEFAULT FALSE,
    ack_note     TEXT,
    ack_at       TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_alerts_timestamp ON sentinel_alerts(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_alerts_level     ON sentinel_alerts(level);
CREATE INDEX IF NOT EXISTS idx_alerts_service   ON sentinel_alerts(service);
CREATE INDEX IF NOT EXISTS idx_alerts_acked     ON sentinel_alerts(acknowledged);
