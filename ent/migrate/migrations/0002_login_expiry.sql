ALTER TABLE ops_account_sessions ALTER COLUMN expires_at SET DEFAULT now()+interval '6 minutes';
