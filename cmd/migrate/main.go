package main

import (
	"context"
	"database/sql"
	"log"
	"os"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/bengobox/subscription-service/internal/config"
	"github.com/bengobox/subscription-service/internal/ent"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	db, err := sql.Open("pgx", cfg.Postgres.URL)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	db.SetMaxIdleConns(2)
	db.SetMaxOpenConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(1 * time.Minute)

	drv := entsql.OpenDB(dialect.Postgres, db)
	client := ent.NewClient(ent.Driver(drv))
	defer client.Close()

	if reset := os.Getenv("SUBSCRIPTION_RESET_DB"); reset == "true" {
		if _, err := db.ExecContext(ctx, "DROP SCHEMA IF EXISTS public CASCADE"); err != nil {
			log.Fatalf("reset schema: %v", err)
		}
		if _, err := db.ExecContext(ctx, "CREATE SCHEMA public"); err != nil {
			log.Fatalf("create schema: %v", err)
		}
	}

	if err := client.Schema.Create(ctx); err != nil {
		log.Fatalf("run migrations: %v", err)
	}

	log.Println("database migrations applied successfully")
}

