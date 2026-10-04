ALTER TABLE wager_transactions
ADD COLUMN reference_attempts INTEGER NOT NULL DEFAULT 0,
ADD COLUMN reference_next_attempt_at TIMESTAMPTZ;

CREATE INDEX wager_transactions_pending_reference_retry_idx
ON wager_transactions (
    reference_next_attempt_at,
    created_at
)
WHERE status = 'PENDING_REFERENCE';