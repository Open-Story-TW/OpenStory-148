-- The native client keeps the migrate-in handle as its own character ID, so
-- the handle is now the character ID. It is unique only among pending tickets.
ALTER TABLE ops_transfers DROP CONSTRAINT IF EXISTS ops_transfers_client_handle_key;
CREATE UNIQUE INDEX ops_transfers_pending_handle ON ops_transfers(client_handle) WHERE consumed_at IS NULL;
