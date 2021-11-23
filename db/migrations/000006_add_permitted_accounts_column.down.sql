DROP INDEX idx_permitted_accounts_gin;

ALTER TABLE connections
    DROP COLUMN permitted_accounts;
