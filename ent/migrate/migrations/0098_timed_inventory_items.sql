ALTER TABLE asset_items ADD COLUMN expires_at bigint NOT NULL DEFAULT 0 CHECK (expires_at >= 0);
