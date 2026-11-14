-- Existing receipts deliberately retain NULL: the old format cannot prove MP
-- or a complete aggregate after an uncertain commit. Never synthesize one.
ALTER TABLE asset_item_uses ADD COLUMN result_bytes bytea;
ALTER TABLE asset_item_uses ADD COLUMN result_sha256 text;
ALTER TABLE asset_item_uses ADD CONSTRAINT item_use_complete_receipt CHECK (
 (result_bytes IS NULL AND result_sha256 IS NULL) OR
 (result_bytes IS NOT NULL AND result_sha256 IS NOT NULL AND octet_length(result_bytes) BETWEEN 1 AND 4194304
  AND result_sha256 ~ '^[0-9a-f]{64}$')
);
