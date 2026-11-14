package main

import (
	"context"
	"database/sql"
	"flag"
	_ "github.com/jackc/pgx/v5/stdlib"
	"log"
	"maple-go-server/ent/migrate"
	"os"
	"time"
)

func main() {
	dsn := flag.String("database-url", os.Getenv("MAPLEGO_DATABASE_URL"), "isolated PostgreSQL URL (prefer environment)")
	flag.Parse()
	if *dsn == "" {
		log.Fatal("MAPLEGO_DATABASE_URL is required")
	}
	db, err := sql.Open("pgx", *dsn)
	if err != nil {
		log.Fatal("database configuration failed")
	}
	defer db.Close()
	db.SetMaxOpenConns(2)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err = migrate.Apply(ctx, db); err != nil {
		log.Fatal(err)
	}
	log.Print("PostgreSQL versioned migrations applied")
}
