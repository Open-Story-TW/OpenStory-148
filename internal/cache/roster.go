package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/redis/go-redis/v9"
	"maple-go-server/internal/domain"
	"time"
)

type Roster struct{ client *redis.Client }

func NewRoster(addr, password string, db int) *Roster {
	if addr == "" {
		return &Roster{}
	}
	return &Roster{client: redis.NewClient(&redis.Options{Addr: addr, Password: password, DB: db, MaxRetries: 0, DialTimeout: 250 * time.Millisecond, ReadTimeout: 150 * time.Millisecond, WriteTimeout: 150 * time.Millisecond, PoolSize: 8, PoolTimeout: 150 * time.Millisecond})}
}
func (r *Roster) Close() error {
	if r.client != nil {
		return r.client.Close()
	}
	return nil
}
func (r *Roster) Ping(ctx context.Context) error {
	if r.client == nil {
		return nil
	}
	return r.client.Ping(ctx).Err()
}
func key(account int64, world int) string {
	return fmt.Sprintf("mgs:local:{a:%d:w:%d}:roster:v2", account, world)
}
func (r *Roster) Get(ctx context.Context, account int64, world int) ([]domain.CharacterSummary, bool) {
	if r.client == nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	raw, err := r.client.Get(ctx, key(account, world)).Bytes()
	if err != nil || len(raw) > 1024*1024 {
		return nil, false
	}
	var result []domain.CharacterSummary
	if json.Unmarshal(raw, &result) != nil {
		return nil, false
	}
	return result, true
}
func (r *Roster) Set(ctx context.Context, account int64, world int, value []domain.CharacterSummary) {
	if r.client == nil {
		return
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > 1024*1024 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	// A bounded display cache: authorization always consults PostgreSQL.
	_ = r.client.Set(ctx, key(account, world), raw, 25*time.Second).Err()
}
func (r *Roster) Invalidate(ctx context.Context, account int64, world int) {
	if r.client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	_ = r.client.Del(ctx, key(account, world)).Err()
}
