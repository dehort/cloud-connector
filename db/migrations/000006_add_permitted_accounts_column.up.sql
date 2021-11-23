ALTER TABLE connections
    ADD permitted_accounts jsonb NOT NULL DEFAULT '{}';

CREATE INDEX idx_permitted_accounts_gin ON connections USING gin (permitted_accounts);
