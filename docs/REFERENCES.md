# 官方參考資料

查閱日期：2026-10-31。下列為技術能力與限制的依據；本計畫的服務分工、SLO、租約協定與容量預算屬本專案設計，需經實作驗證。

| 官方資料 | 用於本計畫 |
| --- | --- |
| [Ent Transactions](https://entgo.io/docs/transactions/) | Ent transaction／transactional client 的提交與回復 |
| [Ent sql.DB Integration](https://entgo.io/docs/sql-integration/) | 自訂 SQL driver 與連線池設定 |
| [Ent Eager Loading](https://entgo.io/docs/eager-load/) | 指定所需 associations，仍檢查 SQL 數與讀取範圍 |
| [Ent Versioned Migrations](https://entgo.io/docs/versioned/intro/) | SQL migration 先產生、審查與版本管理 |
| [PostgreSQL Transaction Isolation](https://www.postgresql.org/docs/18/transaction-iso.html) | 預設隔離不自動保證全部跨列商業不變量 |
| [PostgreSQL Explicit Locking](https://www.postgresql.org/docs/18/explicit-locking.html) | 列鎖、鎖定順序、死鎖與重試 |
| [pg_stat_statements](https://www.postgresql.org/docs/18/pgstatstatements.html) | SQL 執行次數、時間與查詢剖析 |
| [Redis Cache-aside](https://redis.io/docs/latest/develop/use-cases/cache-aside/) | 回源、TTL 與有界展示過期 |
| [Redis Client-side Caching](https://redis.io/docs/latest/develop/reference/client-side-caching/) | 失效斷線／競態的限制；第一版不預設全面啟用 |
| [Redis Pub/Sub](https://redis.io/docs/latest/develop/pubsub/) | at-most-once，不能當可靠資產交付 |
| [Redis Persistence](https://redis.io/docs/latest/operate/oss_and_stack/management/persistence/) | AOF／RDB 有各自持久性取捨；cache 不作唯一資產儲存 |
| [Go Diagnostics](https://go.dev/doc/diagnostics) | CPU／heap profile、trace 與 runtime 診斷 |
| [Go singleflight](https://pkg.go.dev/golang.org/x/sync/singleflight) | 同 key 並行回源合併，不能取代跨服務的資產交易 |
| [Go Argon2](https://pkg.go.dev/golang.org/x/crypto/argon2) | 憑證升級實作與資源參數的官方介面 |

版本鎖定於 P1 確認當時支援的 Go、Ent、pgx、go-redis、PostgreSQL、Redis 版本與授權／部署條件，固定依賴及映像 digest。不能把本文件查閱日期當成持續更新的版本保證。
