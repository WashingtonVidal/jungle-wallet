-- Per-wallet locks coordinate application writes; indexes also guard direct SQL.
CREATE UNIQUE INDEX wager_transactions_opening_wallet_unique
 ON wager_transactions(wallet_id) WHERE kind = 'OPENING';
CREATE UNIQUE INDEX wager_transactions_reversal_unique
 ON wager_transactions(reference_transaction_id, kind)
 WHERE status = 'PROCESSED' AND kind IN ('REFUND', 'ROLLBACK');
-- REFUND and ROLLBACK of a BET both return the same debit.
CREATE UNIQUE INDEX wager_transactions_bet_return_unique
 ON wager_transactions(reference_transaction_id)
 WHERE status = 'PROCESSED' AND kind IN ('REFUND', 'ROLLBACK')
 AND reference_transaction_id IS NOT NULL;
