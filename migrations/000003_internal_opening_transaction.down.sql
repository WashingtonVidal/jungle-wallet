ALTER TABLE wager_transactions
DROP CONSTRAINT IF EXISTS wager_transactions_external_fields_check;

ALTER TABLE wager_transactions
ALTER COLUMN external_transaction_id SET NOT NULL,
ALTER COLUMN provider_id SET NOT NULL,
ALTER COLUMN idempotency_key SET NOT NULL,
ALTER COLUMN payload_hash SET NOT NULL,
ALTER COLUMN round_id SET NOT NULL,
ALTER COLUMN game_id SET NOT NULL;