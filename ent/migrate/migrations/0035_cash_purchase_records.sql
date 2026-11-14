CREATE INDEX asset_cash_orders_account_sn ON asset_cash_orders(account_id,sn);
CREATE INDEX asset_cash_feature_purchase_sn ON asset_cash_feature_operations(account_id,((result#>>'{Product,SN}')))
WHERE result#>>'{Product,SN}' IS NOT NULL AND result#>>'{Product,SN}' <> '0';
