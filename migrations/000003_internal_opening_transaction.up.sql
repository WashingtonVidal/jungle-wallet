ALTER TABLE wager_transactions
ALTER COLUMN external_transaction_id DROP NOT NULL,
ALTER COLUMN provider_id DROP NOT NULL,
ALTER COLUMN idempotency_key DROP NOT NULL,
ALTER COLUMN payload_hash DROP NOT NULL,
ALTER COLUMN round_id DROP NOT NULL,
ALTER COLUMN game_id DROP NOT NULL;

ALTER TABLE wager_transactions
ADD CONSTRAINT wager_transactions_external_fields_check
CHECK (
    (
        kind = 'OPENING'
        AND external_transaction_id IS NULL
        AND provider_id IS NULL
        AND idempotency_key IS NULL
        AND payload_hash IS NULL
        AND round_id IS NULL
        AND game_id IS NULL
    )
    OR
    (
        kind <> 'OPENING'
        AND external_transaction_id IS NOT NULL
        AND provider_id IS NOT NULL
        AND idempotency_key IS NOT NULL
        AND payload_hash IS NOT NULL
        AND round_id IS NOT NULL
        AND game_id IS NOT NULL
    )
);