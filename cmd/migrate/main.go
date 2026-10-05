package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"jungle-wallet/migrations"
)

func run() error {
	if os.Getenv("DATABASE_URL") == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := pgx.Connect(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer db.Close(ctx)
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(731426901)"); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations(version integer PRIMARY KEY)"); err != nil {
		return err
	}
	mode := "up"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	if strings.HasPrefix(mode, "baseline=") {
		version, err := strconv.Atoi(strings.TrimPrefix(mode, "baseline="))
		if err != nil || version != 3 {
			return fmt.Errorf("only baseline=3 is supported for the pre-runner checkpoint")
		}
		var applied int
		if err := tx.QueryRow(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&applied); err != nil {
			return err
		}
		if applied != 0 {
			return fmt.Errorf("baseline requires an empty migration history")
		}
		var exists bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_name='wager_transactions' AND column_name='reference_next_attempt_at')").Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("baseline requires the existing checkpoint schema")
		}
		if _, err = tx.Exec(ctx, "INSERT INTO schema_migrations(version) VALUES(1),(2),(3)"); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if mode != "up" && mode != "down" {
		return fmt.Errorf("usage: migrate [up|down|baseline=3]")
	}
	entries, err := migrations.Files.ReadDir(".")
	if err != nil {
		return err
	}
	latest := 0
	if err := tx.QueryRow(ctx, "SELECT COALESCE(MAX(version),0) FROM schema_migrations").Scan(&latest); err != nil {
		return err
	}
	for _, e := range entries {
		suffix := ".up.sql"
		if mode == "down" {
			suffix = ".down.sql"
		}
		if !strings.HasSuffix(e.Name(), suffix) {
			continue
		}
		version, err := strconv.Atoi(strings.SplitN(e.Name(), "_", 2)[0])
		if err != nil {
			return err
		}
		if mode == "up" && version <= latest {
			continue
		}
		if mode == "down" && version != latest {
			continue
		}
		sql, err := migrations.Files.ReadFile(e.Name())
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("migration %s: %w", e.Name(), err)
		}
		if mode == "up" {
			_, err = tx.Exec(ctx, "INSERT INTO schema_migrations(version) VALUES($1)", version)
		} else {
			_, err = tx.Exec(ctx, "DELETE FROM schema_migrations WHERE version=$1", version)
		}
		if err != nil {
			return err
		}
		fmt.Printf("applied %s\n", e.Name())
	}
	return tx.Commit(ctx)
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
