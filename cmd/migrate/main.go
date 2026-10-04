package main

import (
	"context"
	"database/sql"
	"log"
	"os"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/schema"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/bengobox/subscription-service/internal/config"
	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/migrate"
)

// migrationLockKey serializes migrations across pods: every replica runs this binary on start
// and concurrent ent schema diffs can race on the same DDL (the pos-api 2026-07-26 outage).
// Arbitrary, stable and unique per service ("SUBM", subscriptions migrate).
const migrationLockKey int64 = 0x5355_424D

func main() {
	// Waiting for another pod's migration can take longer than the migration itself.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// Prefer direct PostgreSQL URL to bypass PgBouncer during migrations.
	dbURL := cfg.Postgres.URL
	if cfg.Postgres.MigrateURL != "" {
		dbURL = cfg.Postgres.MigrateURL
	} else {
		log.Printf("WARNING: POSTGRES_MIGRATE_URL is not set; migrating through POSTGRES_URL")
	}

	db, err := sql.Open("pgx", dbURL)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	// One connection: the advisory lock and every migration statement share one session.
	db.SetMaxIdleConns(1)
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(1 * time.Minute)

	drv := entsql.OpenDB(dialect.Postgres, db)
	client := ent.NewClient(ent.Driver(drv))
	defer client.Close()

	if _, err := db.ExecContext(ctx, "SELECT pg_advisory_lock($1)", migrationLockKey); err != nil {
		log.Fatalf("acquire migration lock: %v", err)
	}
	unlock := func() {
		if _, err := db.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", migrationLockKey); err != nil {
			log.Printf("release migration lock: %v", err)
		}
	}

	if reset := os.Getenv("SUBSCRIPTION_RESET_DB"); reset == "true" {
		if _, err := db.ExecContext(ctx, "DROP SCHEMA IF EXISTS public CASCADE"); err != nil {
			log.Fatalf("reset schema: %v", err)
		}
		if _, err := db.ExecContext(ctx, "CREATE SCHEMA public"); err != nil {
			log.Fatalf("create schema: %v", err)
		}
	}

	migrateErr := client.Schema.Create(ctx, schema.WithDir(migrate.Dir))
	if migrateErr == nil {
		migrateErr = migrate.DropLegacyIndexes(ctx, db)
	}
	unlock()
	if migrateErr != nil {
		log.Fatalf("run migrations: %v", migrateErr)
	}

	log.Println("database migrations applied successfully")
}
