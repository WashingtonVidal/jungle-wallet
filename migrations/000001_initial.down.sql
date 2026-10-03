DROP TRIGGER IF EXISTS wallet_ledger_prevent_delete
    ON wallet_ledger_entries;

DROP TRIGGER IF EXISTS wallet_ledger_prevent_update
    ON wallet_ledger_entries;

DROP FUNCTION IF EXISTS prevent_wallet_ledger_mutation();

DROP TABLE IF EXISTS outbox;
DROP TABLE IF EXISTS inbox;
DROP TABLE IF EXISTS wallet_ledger_entries;
DROP TABLE IF EXISTS wager_transactions;
DROP TABLE IF EXISTS wallets;