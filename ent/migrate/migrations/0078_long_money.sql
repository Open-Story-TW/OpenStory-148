-- Let the application's long_money switch decide the balance ceiling for
-- mesos, storage mesos and the cash wallet. The columns are already bigint;
-- with the switch off the application still stops at the 32-bit ceiling, so
-- this does not change existing behavior. Ground meso drops stay 32-bit.
ALTER TABLE game_characters DROP CONSTRAINT IF EXISTS game_characters_mesos_check;
ALTER TABLE game_characters ADD CONSTRAINT game_characters_mesos_check CHECK (mesos >= 0);
ALTER TABLE asset_meso_ledger DROP CONSTRAINT IF EXISTS asset_meso_ledger_balance_after_check;
ALTER TABLE asset_meso_ledger ADD CONSTRAINT asset_meso_ledger_balance_after_check CHECK (balance_after >= 0);
ALTER TABLE asset_storages DROP CONSTRAINT IF EXISTS asset_storages_mesos_check;
ALTER TABLE asset_storages ADD CONSTRAINT asset_storages_mesos_check CHECK (mesos >= 0);
ALTER TABLE asset_cash_wallets DROP CONSTRAINT IF EXISTS asset_cash_wallets_credit_check;
ALTER TABLE asset_cash_wallets ADD CONSTRAINT asset_cash_wallets_credit_check CHECK (credit >= 0);
ALTER TABLE asset_cash_wallets DROP CONSTRAINT IF EXISTS asset_cash_wallets_points_check;
ALTER TABLE asset_cash_wallets ADD CONSTRAINT asset_cash_wallets_points_check CHECK (points >= 0);
ALTER TABLE asset_cash_wallets DROP CONSTRAINT IF EXISTS asset_cash_wallets_prepaid_check;
ALTER TABLE asset_cash_wallets ADD CONSTRAINT asset_cash_wallets_prepaid_check CHECK (prepaid >= 0);
