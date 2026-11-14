# PostgreSQL 與 Ent 資料設計

## 儲存技術與權限

採 PostgreSQL、Go 的 Ent ORM（`entgo.io/ent`），透過 pgx 的 `database/sql` 相容介面共用每程序連線池。Ent 產生型別化 schema／query；遊戲 domain struct 與封包 DTO 保持獨立。需要批次更新、條件扣款或 fencing 的 SQL 可以封裝於 repository，仍必須參與同一個 transaction。

Ent 的交易、連線池整合與版本化 migration 依官方文件實作，見 [參考資料](REFERENCES.md)。各服務啟動不自行執行 `Schema.Create()`；由獨立 migrator 套用審查過的 SQL，先採擴充相容再移除舊欄位。

第一版用一個 PostgreSQL cluster／database，依 `auth_`、`game_`、`asset_`、`cash_`、`ops_` 前綴及服務 role 分工。這讓商城交付可使用單一 ACID transaction。各世界的資料帶 `world_id`，repository 必須有世界範圍；服務 role／受限資產轉移介面與必要的 row policy 防止跨界寫入，需整合測試驗證。未來按世界拆實體資料庫時，資產交付須改為 escrow／outbox 協定，不能沿用跨 DB 的普通 transaction 假設。

## 核心 Ent entity 規劃

| 表／entity | 核心欄位與約束 | 擁有者 |
| --- | --- | --- |
| auth_accounts／credentials | account_id、正規化帳號、hash_scheme、password_hash、pin_hash、status、auth_revision；帳號鍵唯一 | login |
| auth_bans／roles | 帳號封鎖、到期、權限與變更審計；新帳號預設普通玩家 | login／管理操作 |
| game_worlds | world_id、名稱、啟用、容量、資料版本 | 配置／受審計管理操作 |
| game_characters | character_id、account_id、world_id、name、name_key、job、appearance、revision、deleted_at | 該 world |
| game_character_progress | character_id、level、exp、HP／MP、能力值、map／spawn、persisted_seq、revision | 該 world |
| game_character_roster | account_id、world_id、character_id、slot、封包所需的 stats／外觀／排行、summary_revision | world 寫；login 讀 |
| game_character_skills | character_id、skill_id、level、master_level、expires_at；角色＋技能唯一 | world |
| game_character_quests／quest_mobs | 角色＋任務／怪物唯一；狀態、計數、custom_data、revision | world |
| game_character_settings | keymap、macro、傳點、偏好；元件型別與版本 | world |
| game_wallets | character_id、currency、balance、revision；餘額與客戶端上限檢查 | world 的經濟命令 |
| asset_containers | container_id、account／character／world、kind、capacity、revision | 依 container 類型授權 |
| asset_items | 永久 item_instance_id、template_id、container_id、slot、quantity、expires_at、revision；容器＋slot 唯一 | 目前 container 的單一寫入者 |
| asset_equipment | item_instance_id 一對一、裝備數值、潛能、星力、revision | 與物品相同 |
| asset_movements | operation_id、物品、來源／目的容器、原因、前後版本；冪等唯一鍵 | 受限移轉 repository |
| cash_wallets／ledger | account_id、currency、balance、revision；每次增減有來源與 operation_id | cashshop |
| cash_catalog／stock | SKU、content_version、價格、期間、限量、伺服器可購買規則 | cashshop／內容管理 |
| cash_orders／deliveries | order_id、idempotency_key、價目版本、價格、狀態、唯一交付紀錄 | cashshop |
| game_guilds／members／buddies | world 範圍、成員唯一、權限與版本；依實際跨世界需求調整 | world |
| ops_account_sessions | account_id 唯一、活動 character、owner instance、generation、狀態 | 經驗證的服務交接 |
| ops_character_ownership | character_id 唯一、world、runtime epoch、session generation、狀態 | runtime／交接契約 |
| ops_runtime_leases | 世界活動 instance、epoch、到期；世界唯一活動租約 | runtime |
| ops_transfers | transfer_id、憑證 hash、source／target、generation、expiry、consumed_at | 交接契約 |
| ops_outbox／consumer_receipts | event_id、aggregate/version、payload version、重試／接收紀錄 | 提交者／worker |
| ops_audit_log／import_runs | 操作人、原因、結果、來源 checksum／mapping version | 審計／匯入工具 |

此表是邏輯模型；詳細欄位由 P0 封包欄位對照與舊表盤點完成後，於 P1 寫成 Ent schema。角色摘要需要覆蓋 v145 `addCharStats` 與 `addCharLook` 的實際欄位，不只名字、等級與職業。

## 資料型別與不變量

- ID／內部數值優先明確使用 Go int64／PG bigint；客戶端可見 ID 依既有欄位位寬限制，不能靜默截斷。角色 ID 先限制於已驗證的有號 32 位元範圍；物品序號依封包欄位逐一對照。
- 楓幣／點數用整數，不用浮點。資料庫 check 與條件更新阻止負餘額；本機 v145 meso32 的可見／交易上限必須一起實施。內部 bigint 不代表客戶端已支援超過 int32 的楓幣。
- 日期用 UTC `timestamptz`，沒有期限使用 NULL／明確 enum；匯入時逐欄區分秒、毫秒、FILETIME 與 -1 哨兵。
- 名稱保存顯示值及正規化 key，制定中文／大小寫／重音比較規則。預設角色名稱全服唯一，與跨世界查名規則一起實作；來源 SQL 沒有名稱唯一約束，匯入前必須找衝突。
- 關係、餘額、物品歸屬與任務狀態用可約束的欄位／子表。版本化外觀 payload、非核心擴充資料才使用 JSONB，避免把整隻角色塞進單一大 JSON。
- 每個物品實體永遠只有一個 container；跨商城／角色倉庫的轉移更新同一列，寫入 movement 與唯一 operation_id。
- 角色刪除使用狀態轉換／保留期；資產與訂單不可因任意 cascade 遺失審計。具體保留期在營運規則確定時設定。
- 環境內新角色預設一般玩家；Java 基線的創角 GM 100、測試倍率及無限墜飾不作為 Go 正式預設。

## 查詢及索引預算

| 流程 | 目標查詢方式／索引 |
| --- | --- |
| 帳號登入 | 帳號正規化鍵唯一索引；只讀驗證／封鎖必要欄位 |
| 角色列表 | `game_character_roster(account_id, world_id, slot)`；cache miss 以 1–2 次投影查詢完成，數量不隨任務／物品數放大 |
| 選角 | 驗證 account＋world＋character 歸屬，取得租約後只讀選中角色；核心 aggregate ≤10 次批次 SQL，擴充功能須更新預算 |
| 背包／任務 | container＋slot、character＋quest／skill 唯一索引；quest_mobs 以該角色的任務 ID 集合批次讀取 |
| 點數與限量商品 | account＋currency、SKU＋活動期／帳號購買限制索引；條件更新／列鎖 |
| 道具交付／RPC 重試 | operation scope＋operation_id、order_id＋delivery_type 唯一約束 |
| outbox | 未處理／到期重試的局部索引；批次 claim、帶 lease，避免 worker 持鎖等待網路 |

不在 per-character 迴圈內查 edge。Ent eager-loading 仍須檢查產生的 SQL 和讀取量，使用明確 projection／批次；不能用全邊 `With*` 代替需求分析。初次完整讀取以一致的 read-only transaction 取得對應元件版本。

資料量達到門檻時才對 ledger／audit／outbox 歷史表按時間分區；先量測查詢、WAL、vacuum 與索引大小，不按玩家人數預先拆所有角色表。

## 交易與重試

1. transaction 內檢查 runtime fencing、session generation、revision 與授權；涉及多名角色／資產時固定鎖順序。
2. 扣款以餘額條件更新或列鎖檢查，與物品歸屬、order／ledger、outbox 一起 commit。
3. 只有 commit 成功才回覆成功；commit 回覆不明確時，用相同 operation_id 查詢結果再重試。
4. 重試只針對可重試的 serialization／deadlock 等錯誤，有限次、具退避；一般錯誤不可盲目重播。
5. Ent mutation hook 不是交易已提交的證明。cache／外部副作用在 commit 後，可靠失效透過同交易 outbox 補償。
6. Ent transaction 內所有 repository 必須使用該 tx 的 client，避免部分 SQL 意外落在 transaction 外。

多資產交易的鎖定與錯誤處理依 [PostgreSQL 的列鎖說明](https://www.postgresql.org/docs/18/explicit-locking.html)；跨列不變量可選 Serializable 並實作整筆重試，而非假設預設隔離層級自動排除所有競態。

## 5,000 CCU 連線預算

CCU 不等於 DB connection 數。起始壓測配置：PostgreSQL 128 個連線上限、至少保留 32 個給維運／複寫等；應用尖峰總額不超過 96。

| 程序／副本 | 每副本上限 | 尖峰合計範例 |
| --- | ---: | ---: |
| login ×2 | 8 | 16 |
| cashshop ×1 | 16 | 16 |
| world ×2 | 24 | 48 |
| worker ×1 | 8 | 8 |
| migrator／匯入等預留 | 合計 2 | 2 |
| 應用合計 | | 90 |

這是配置起點。擴副本、滾動更新的舊新程序及每個獨立 pool 都需計入公式 `sum(peak_replicas × MaxOpenConns)`。超過預算時必須調整配置／部署順序；不能讓多個服務各自照抄 100 條連線。設定 pool wait、query、lock、transaction deadline；以 pool 等待與 PG profile 調整。PgBouncer 可在量測後導入，transaction pooling／prepared statement 的相容性需測試。

## MariaDB → PostgreSQL 轉換

盤點原始 SQL 的全部 160 張表，產生 [schema inventory](baseline/legacy-schema.json)。每張表／欄位必須標示保留、轉換、改成靜態內容、封存或待實作；不能把暫時沒做的自訂功能直接丟掉。

轉換特別檢查 AUTO_INCREMENT／sequence、unsigned 範圍、enum／tinyint 布林、collation、零日期／哨兵、反引號與保留字、逗號分隔 SP／pets、外鍵缺口、角色名衝突、物品流水號、舊憑證雜湊。保留可對照的 legacy ID 與 importer mapping version；所有匯入可重跑於新目標庫並輸出 counts／sum／checksum。

帳密先以 `hash_scheme` 相容驗證舊演算法，成功登入後再升級至經測試的 Argon2id；hash 工作池限制併發，評估尖峰記憶體與 CPU。第二密碼也獨立處理。不要直接把舊 hash 當成明文再次 hash，或要求所有玩家無預警重設。

詳細切換與回復限制見 [路線圖](ROADMAP.md)。
