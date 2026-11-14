package persistence

import (
	"context"
	"database/sql"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"fmt"
	_ "github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/sync/singleflight"
	"maple-go-server/ent"
	"maple-go-server/internal/cache"
	"maple-go-server/internal/domain"
	"strings"
	"sync/atomic"
	"time"
)

type Config struct {
	DatabaseURL   string
	RedisAddr     string
	RedisPassword string
	RedisDB       int
	MaxOpenConns  int
}
type Store struct {
	db        *sql.DB
	client    *ent.Client
	roster    *cache.Roster
	loads     singleflight.Group
	reads     atomic.Int64
	cacheHits atomic.Int64
}

var _ domain.Repository = (*Store)(nil)

func Open(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("PostgreSQL connection is not configured")
	}
	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid PostgreSQL configuration")
	}
	max := cfg.MaxOpenConns
	if max <= 0 {
		max = 12
	}
	db.SetMaxOpenConns(max)
	db.SetMaxIdleConns(min(max, 4))
	db.SetConnMaxLifetime(30 * time.Minute)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("PostgreSQL connection failed: %T", err)
	}
	var database string
	if err = db.QueryRowContext(ctx, "SELECT current_database()").Scan(&database); err != nil || !strings.HasPrefix(database, "maple_go_") {
		db.Close()
		return nil, fmt.Errorf("refusing database outside maple_go_ namespace")
	}
	driver := entsql.OpenDB(dialect.Postgres, db)
	return &Store{db: db, client: ent.NewClient(ent.Driver(driver)), roster: cache.NewRoster(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)}, nil
}
func (s *Store) Close() error                          { _ = s.roster.Close(); return s.client.Close() }
func (s *Store) Ping(ctx context.Context) error        { return s.db.PingContext(ctx) }
func (s *Store) CachePing(ctx context.Context) error   { return s.roster.Ping(ctx) }
func (s *Store) Stats() (rosterReads, cacheHits int64) { return s.reads.Load(), s.cacheHits.Load() }
func (s *Store) Database() *sql.DB                     { return s.db }
