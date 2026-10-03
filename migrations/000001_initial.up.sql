-- Initial PostgreSQL schema for Jungle Wallet.
-- Financial correctness must not depend only on application memory.

CREATE TABLE wallets (
    id TEXT PRIMARY KEY,
    player_id TEXT NOT NULL,
    currency VARCHAR(3) NOT NULL,
    balance_units BIGINT NOT NULL,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,

    CONSTRAINT wallets_currency_check
        CHECK (currency IN ('BRL', 'USD', 'EUR')),

    CONSTRAINT wallets_balance_nonnegative_check
        CHECK (balance_units >= 0),

    CONSTRAINT wallets_version_positive_check
        CHECK (version >= 1),

    CONSTRAINT wallets_player_currency_unique
        UNIQUE (player_id, currency)
);

CREATE TABLE wager_transactions (
    id TEXT PRIMARY KEY,
    external_transaction_id TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    payload_hash TEXT NOT NULL,

    wallet_id TEXT NOT NULL
        REFERENCES wallets(id),

    player_id TEXT NOT NULL,
    round_id TEXT NOT NULL,
    game_id TEXT NOT NULL,

    kind TEXT NOT NULL,
    amount_units BIGINT NOT NULL,
    currency VARCHAR(3) NOT NULL,

reference_external_transaction_id TEXT,
reference_transaction_id TEXT
    REFERENCES wager_transactions(id),

status TEXT NOT NULL,

    result_balance_units BIGINT,
    failure_code TEXT,
    failure_message TEXT,

    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,

    CONSTRAINT wager_transactions_kind_check
        CHECK (
            kind IN (
                'OPENING',
                'BET',
                'WIN',
                'LOSS',
                'REFUND',
                'ROLLBACK'
            )
        ),

    CONSTRAINT wager_transactions_status_check
        CHECK (
            status IN (
                'PENDING',
                'PENDING_REFERENCE',
                'PROCESSED',
                'REJECTED',
                'FAILED'
            )
        ),

    CONSTRAINT wager_transactions_currency_check
        CHECK (currency IN ('BRL', 'USD', 'EUR')),

    CONSTRAINT wager_transactions_amount_nonnegative_check
        CHECK (amount_units >= 0),

    CONSTRAINT wager_transactions_result_balance_nonnegative_check
        CHECK (
            result_balance_units IS NULL
            OR result_balance_units >= 0
        ),

    CONSTRAINT wager_transactions_provider_external_unique
        UNIQUE (provider_id, external_transaction_id),

    CONSTRAINT wager_transactions_provider_idempotency_unique
        UNIQUE (provider_id, idempotency_key)
);

CREATE INDEX wager_transactions_reference_idx
    ON wager_transactions (
        provider_id,
        reference_external_transaction_id
    )
    WHERE reference_external_transaction_id IS NOT NULL;

CREATE TABLE wallet_ledger_entries (
    id TEXT PRIMARY KEY,

    wallet_id TEXT NOT NULL
        REFERENCES wallets(id),

    transaction_id TEXT NOT NULL
        REFERENCES wager_transactions(id),

    direction TEXT NOT NULL,
    amount_units BIGINT NOT NULL,
    currency VARCHAR(3) NOT NULL,

    balance_before_units BIGINT NOT NULL,
    balance_after_units BIGINT NOT NULL,

    created_at TIMESTAMPTZ NOT NULL,

    CONSTRAINT wallet_ledger_direction_check
        CHECK (direction IN ('DEBIT', 'CREDIT')),

    CONSTRAINT wallet_ledger_currency_check
        CHECK (currency IN ('BRL', 'USD', 'EUR')),

    CONSTRAINT wallet_ledger_amount_positive_check
        CHECK (amount_units > 0),

    CONSTRAINT wallet_ledger_before_nonnegative_check
        CHECK (balance_before_units >= 0),

    CONSTRAINT wallet_ledger_after_nonnegative_check
        CHECK (balance_after_units >= 0),

    CONSTRAINT wallet_ledger_transaction_unique
        UNIQUE (wallet_id, transaction_id),

    CONSTRAINT wallet_ledger_balance_equation_check
        CHECK (
            (
                direction = 'DEBIT'
                AND balance_after_units = balance_before_units - amount_units
            )
            OR
            (
                direction = 'CREDIT'
                AND balance_after_units = balance_before_units + amount_units
            )
        )
);

CREATE INDEX wallet_ledger_wallet_created_idx
    ON wallet_ledger_entries (wallet_id, created_at, id);

CREATE TABLE inbox (
    id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL,
    external_transaction_id TEXT NOT NULL,
    payload_hash TEXT NOT NULL,
    payload JSONB NOT NULL,

    status TEXT NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,

    last_error TEXT,

    received_at TIMESTAMPTZ NOT NULL,
    processed_at TIMESTAMPTZ,

    CONSTRAINT inbox_status_check
        CHECK (
            status IN (
                'RECEIVED',
                'PROCESSING',
                'PROCESSED',
                'FAILED'
            )
        ),

    CONSTRAINT inbox_attempts_nonnegative_check
        CHECK (attempts >= 0),

    CONSTRAINT inbox_provider_external_unique
        UNIQUE (provider_id, external_transaction_id)
);

CREATE TABLE outbox (
    id TEXT PRIMARY KEY,

    aggregate_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL,
    event_type TEXT NOT NULL,

    payload JSONB NOT NULL,

    attempts INTEGER NOT NULL DEFAULT 0,
    available_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ,

    last_error TEXT,

    CONSTRAINT outbox_attempts_nonnegative_check
        CHECK (attempts >= 0)
);

CREATE INDEX outbox_pending_idx
    ON outbox (available_at, created_at)
    WHERE published_at IS NULL;

-- The ledger is append-only.
-- PostgreSQL rejects UPDATE and DELETE even if application code tries them.

CREATE OR REPLACE FUNCTION prevent_wallet_ledger_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'wallet ledger is append-only';
END;
$$;

CREATE TRIGGER wallet_ledger_prevent_update
BEFORE UPDATE ON wallet_ledger_entries
FOR EACH ROW
EXECUTE FUNCTION prevent_wallet_ledger_mutation();

CREATE TRIGGER wallet_ledger_prevent_delete
BEFORE DELETE ON wallet_ledger_entries
FOR EACH ROW
EXECUTE FUNCTION prevent_wallet_ledger_mutation();