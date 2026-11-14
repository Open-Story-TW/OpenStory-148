# 其他優化與優先順序

以下為這個資料包與 5,000 CCU 目標的工程提案。原始碼已確認讀寫放大；硬體瓶頸、實際收益及容量都仍需量測。

| 優先 | 項目 | 為什麼先做／驗證方式 |
| --- | --- | --- |
| P0 | 角色摘要 projection、選中才載入 | 已發現 login 逐角色載入及逐任務 mob 查詢；以查詢數與傳輸量驗證，cache miss 仍應便宜 |
| P0 | 增量保存、錯開 checkpoint | 舊程式有全量刪除重寫；量測每角色保存列數、WAL、鎖等待與保存 lag |
| P0 | 單一寫入權、冪等交易、資產對帳 | 多程序後最容易出現的資料完整性問題；用雙登入、交接重送、crash-at-commit 驗證 |
| P0 | 有界 queue、DB pool、回源／登入 hash 限流 | 避免 5,000 人重連或 Redis 故障變成雪崩；觀察 queue age、pool wait 與受控 busy |
| P0 | 從第一階段建立可觀測性 | 讓 profile 決定真正瓶頸；每個 world、channel、opcode 有延遲與錯誤統計 |
| P1 | 地圖 AOI／空間索引與廣播裁剪 | 人多同圖時全對全傳送接近平方放大；依可見範圍做 spawn／despawn、技能／怪物通知，驗證 v145 行為 |
| P1 | 地圖／角色單一寫入者 | 減少細粒度鎖與共享競態；race test、跨圖／跨玩家資產測試 |
| P1 | 不可變靜態資料包、按需地圖實例 | 道具／技能規則本地查表、地圖無人時回收；測冷啟動／RSS、內容版本一致性 |
| P1 | 腳本相容層、執行期限與 API 約束 | Nashorn 腳本大量依賴 Java；先盤點 API，再選 VM／改寫策略，不能直接替換引擎當完成 |
| P1 | 統一 timer／工作排程 | 依實測使用分桶／heap／時間輪，避免每怪物每 Buff 都建立獨立高頻工作；測 timer backlog |
| P1 | 封包 allocation／GC 與慢連線處理 | 減少反覆 encode／copy、共享不可變廣播 payload；先 profile，再採 buffer pool，避免提前回收被持用 buffer |
| P1 | PG 索引、慢 SQL、vacuum 與連線預算 | 觀察 query plan、WAL、dead tuples、鎖等待；不是換 PostgreSQL／Ent 就會自動變快 |
| P1 | 獨立部署、世界 drain、資源限制 | 降低 OOM／故障擴散；每個 world 有自己的 restart／CPU／RAM 設定與啟動識別 |
| P1 | 備份還原／primary failover 演練 | 確認玩家資產能恢復，而非只檢查備份檔存在 |
| P2 | 排行榜、歷史紀錄與分析異步化 | 從遊戲熱路徑移走報表／全服掃描；用 outbox、增量投影與時間分區 |
| P2 | 經 profile 證明後才分更多服務／分 DB | 熱門世界、社交、拍賣或頻道可再拆；先定義 ownership／資產跨庫協定與新增故障行為 |

## 5,000 CCU 的熱點不是平均人數

兩世界平均 2,500 人，不代表每張圖只有少量玩家。單一活動地圖、世界廣播、Boss 召喚與多人技能會同時放大 CPU 與網路。

AOI 採空間格／視野訂閱，保持玩家、怪物、掉落物的可見集合；地圖上限與超額入場策略由壓測結果制定。遠端隊友狀態、全圖技能、怪物 controller／攻擊合法性需要額外規則，不能單純丟棄距離遠的封包就視為相容。

限制客戶端封包長度、操作速率與狀態機；重複／越界 opcode 在解碼邊界拒絕。伺服器驗證拾取、金額、技能冷卻、移動與傷害規則，客戶端 DLL 提供的數值不具持久資產權威。

## 監控與效能診斷

至少收集：

- 各服務／世界 CCU、登入／離線／換頻原因、handoff 成功率／逾時。
- handler／map executor latency、queue length／age、每 opcode bytes／errors、慢客戶端數。
- 每角色 checkpoint 次數／變動列數、dirty age、失敗重試、revision／epoch 拒絕次數。
- 每流程 SQL 次數與資料量、pool wait、query P95／P99、PG 鎖等待、WAL／IO／vacuum。
- Redis hit／miss、fallback、eviction、payload 大小、cold-fill 並行量。
- ledger／delivery 對帳差異、outbox lag／重播次數／未決 operation。
- Go heap／allocations／GC pauses、goroutines、CPU、程序 restart 與 OOM。

使用 structured log、Prometheus 指標、抽樣 OpenTelemetry trace；character ID／account ID 放在受控 log／trace context，不能作為無界 metric label。帳密、PIN、憑證與完整封包不得寫入一般日誌。pprof／管理介面只在受限網路啟用。

Go 官方提供 CPU／heap profiling、execution tracing 與 runtime diagnostics，詳見 [Go Diagnostics](https://go.dev/doc/diagnostics)。先量測再調 `GOMEMLIMIT`、工作池、pool／buffer 或網路模型。

## 實驗環境與資源估算方式

首輪壓測的候選配置，屬實驗起點，非達標保證：

| 元件 | 初始資源區間 |
| --- | --- |
| login 副本 | 每副本 1–2 vCPU／1–2 GiB RAM；hash 併發另算 |
| cashshop | 2–4 vCPU／2–4 GiB |
| 每個 world | 4–8 vCPU／8–16 GiB；另做單世界 5,000 負載 |
| PostgreSQL primary | 4–8 vCPU／16–32 GiB、可量測延遲的 NVMe |
| Redis cache | 1–2 vCPU／1–4 GiB，依實際 payload／熱鍵數調整 |
| worker | 1–2 vCPU／1–2 GiB |

計算 world 記憶體時，用 `靜態內容 + 實測角色 aggregate × 在線數 + 活躍地圖／怪物 + 有界網路佇列 + GC 餘裕`。角色大小須取有背包／任務的代表值與高分位，不以空角色或 socket 數估算。

一台較大的開發機可以先做程序與功能驗證；正式測主機故障隔離時，世界／共用資料層必須分散故障域。讀 replica 優先給報表／可延遲投影；購買、交接、最新寫入權與 read-your-writes 要走權威 primary。

## 架構取捨

第一版保留一世界一程序、同 PG 內的原子資產移轉、PG outbox 與 Redis 可重建快取。Kubernetes、Kafka、全量 event sourcing、每頻道一程序及依世界分實體資料庫，都列為有量測／隔離需求後再導入的選項。每次增加基礎元件，都要有相應的維運與故障驗收。
