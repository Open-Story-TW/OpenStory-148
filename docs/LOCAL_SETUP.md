# Go 伺服器本機實作

## 已交付

- Go 的 `login.exe`、`world.exe`、`cashshop.exe` 為獨立程序；同一 `world.exe` binary 可啟動不同 world ID。
- 本機啟動兩個世界，各 11 個 listener／頻道；支援冒險家創角、角色列表與進圖。同世界 ch1→ch2→ch1 已由雙角色登入器 MCP 通過基本案例。
- PostgreSQL 18.6、Redis 8.2 系列使用獨立 Docker Compose project `maple-go-local`，只發布 loopback 端口，映像 digest 保存於 `.local/compose.env`。
- Ent v0.14.6 schema 與 generated code；獨立 migrator 套用帶 checksum 的版本化 SQL，服務啟動不自行建表。新增持久化包含背包／交易／倉庫／任務／腳本 log／技能／商城。
- Argon2id 帳密／第二密碼、性別設定、普通玩家權限、創角白名單、四件獨立流水號裝備。
- 角色列表讀專用 roster projection；冷讀 1 SQL，Redis hit 不重新查角色詳細資料；Redis 失效時可受 deadline 控制地回源。
- PG 世界租約、遞增 epoch、session generation、一次性 handoff、revision 檢查；新角色建立透過已驗證的 protobuf/gRPC 呼叫世界程序。

2026-10-31 實機：使用者在 Go 測試帳號 `golocal` 建立角色 `GoQA`；隨後由 computer-use 選角，客戶端顯示楓之島地圖 10000。Go 世界日誌記錄 character 18 進圖，實際遊戲連線到 `127.0.0.1:18485` 的 `world.exe`。PostgreSQL 核對為 GM 0、四件初始裝備。帳號與角色 ID 是這次環境的紀錄，其他新環境可能不同。

目前功能與實機範圍以 [P3–P5 驗收矩陣](P3-P5-ACCEPTANCE.md) 為準。以下舊測試段落是當時的歷史結果，不代表後續所有功能已通過登入器 MCP。

2026-11-01 增量：接通 `USE_ITEM (0x72)` 的紅水 `2000000`，每次扣 1 瓶、回復 50 HP 並限制於 MaxHP。數值來源為 Java 基線 `wz/Item.wz/Consume/0200.img.xml` 的 `02000000/spec/hp`；封包對照 `InventoryHandler.UseItem`、`CWvsContext.InventoryPacket.modifyInventory` 與 `CWvsContext.updatePlayerStats`。更新數量／清空欄位使用 `0x27`，HP 使用 `0x29` 的 `0x400` mask。

扣道具、HP、角色 revision、roster 與使用收據同一筆 PG transaction 提交。收據以角色／登入 session／generation／client tick 去重，檢查原始 slot／template；耗盡後仍能處理重送。不同 session 不共用 tick。未知消耗品、空欄位、死亡、失效擁有者與禁止地圖 `749040100` 拒絕使用。角色的 checkpoint、紅水與楓幣操作序列化，保留記憶體中的移動狀態，避免舊 revision／HP 覆蓋新交易。

本次已通過 `go test -mod=readonly -count=1 ./...`（啟用真實 PG／Redis integration）、`go vet ./...` 及 `tools/local.ps1 test`。案例涵蓋 6 個並行相同請求、注入收據寫入失敗的全交易 rollback、最後一瓶移除、重送不重扣、重登保存與 checkpoint 競態；TCP smoke 會實際用完新測試角色的 5 瓶紅水並逐次核對封包。此次沒有操作圖形客戶端，因此不將紅水畫面標記為實機通過。最新 TCP 結果在 `.local/verification.json`。

## 操作

需要 Docker Desktop Linux containers、PowerShell 與 Go。`go.mod` 固定 toolchain `go1.27.3`；既有系統 Go 可以自動下載此 toolchain，沒有替換系統預設 Java 或 Go。

```powershell
# 只建立／啟動 Go 專用 PostgreSQL 與 Redis
powershell.exe -NoProfile -ExecutionPolicy Bypass -File tools/local.ps1 setup

# 建置服務、migrator 與協定測試工具
powershell.exe -NoProfile -ExecutionPolicy Bypass -File tools/local.ps1 build

# 啟動基礎服務、套用 migration，啟動所有 Go 程序
powershell.exe -NoProfile -ExecutionPolicy Bypass -File tools/local.ps1 start

# Go 已建置時啟動，可指定單一服務
powershell.exe -NoProfile -ExecutionPolicy Bypass -File tools/local.ps1 start -SkipBuild
powershell.exe -NoProfile -ExecutionPolicy Bypass -File tools/local.ps1 start -Service world1 -SkipBuild

# 查詢 PID、health 與 Java 對照程序識別
powershell.exe -NoProfile -ExecutionPolicy Bypass -File tools/local.ps1 status

# 真實加密 TCP／PG／Redis 整合測試，並產生本機圖形測試帳號
powershell.exe -NoProfile -ExecutionPolicy Bypass -File tools/local.ps1 test

# 複製並啟動獨立遊戲，原客戶端與原 DLL 不修改
powershell.exe -NoProfile -ExecutionPolicy Bypass -File tools/client.ps1 start

# 正常停止 Go 程序
powershell.exe -NoProfile -ExecutionPolicy Bypass -File tools/local.ps1 stop

# 也停止 Go 的 Docker 容器；保留 PostgreSQL volume
powershell.exe -NoProfile -ExecutionPolicy Bypass -File tools/local.ps1 stop -StopInfrastructure
```

`start-local.cmd`、`start-client.cmd`、`stop-local.cmd` 是以上命令的捷徑。建置正在使用中的 binary 時會要求先停止對應 Go 程序，避免替換運行中的檔案。正常停止透過每程序的 stop file，使用 PID、路徑與啟動時間核對，不操作 Java。

## 端口

| 服務 | Go 位址 |
| --- | --- |
| 登入 | `127.0.0.1:18484` |
| world 0 ch1–11 | `127.0.0.1:18485..18495` |
| world 1 ch1–11 | `127.0.0.1:18505..18515` |
| 商城骨架 | `127.0.0.1:18600` |
| PostgreSQL | `127.0.0.1:15432`，database `maple_go_local` |
| Redis | `127.0.0.1:16379` |
| login／world0／cashshop／world1 health | `19080`／`19081`／`19082`／`19083`，路徑 `/health/live`、`/health/ready` |
| world0／world1 內部 RPC | `127.0.0.1:19485`／`127.0.0.1:19505` |

Java 對照與 MariaDB 使用自己的 listener。`.local/java-baseline.json` 記錄本次工作開始時的 PID／啟動時間／執行檔／端口；本機腳本核對它們仍一致。2026-11-01 的 Java 參考是 OpenMaple MCP 隔離副本（登入 `28484`、MariaDB `23307`），不再是上一次工作的 PID；舊基線已保留於 `.local/java-baseline-before-item-use-*.json`。Go 工具不啟停 Java、不查寫 MariaDB，亦不更換原客戶端檔案。

## 私有設定與可重建產物

- `.local/environment.json`：Go 專用 PG／Redis 憑證、RPC token、世界目錄；不要輸出到一般日誌或提交。
- `.local/secrets/`、`.local/redis.conf`：Compose 的私有設定。PG 使用 Docker named volume，刪除本機憑證檔不會重設既有 DB 密碼。
- `.local/client-test.credentials.json`：圖形測試帳號、密碼與第二密碼。
- `.local/client/`：獨立複製的客戶端檔案，沒有 hardlink；`.local/client-preparation.json` 記錄 WZ／EXE／DLL checksum。
- `.local/bin/`、`.local/run/`、`.local/logs/`：程式、程序識別與輸出。
- `.local/verification.json`、`.local/gui-verification.json`、`.local/isolation-verification.json`：分別為 TCP、實機、程序故障測試紀錄；時間與 artifact hash 必須配對解讀。
- `.local/evidence/go-world-entry.png`：實際 Go 進圖畫面。

世界目錄由 `MAPLEGO_WORLDS_JSON` 決定；單一世界使用 `MAPLEGO_WORLD_ID` 或 `world.exe --world-id N`。所有目前的服務、DB 與 RPC 配置均強制 loopback，並限制 DB 名稱 `maple_go_*`。

`MAPLEGO_WZ_ROOT` 指向唯讀 WZ XML 目錄；本機目前使用相鄰 Java 參考專案的 `wz`，不改寫來源。未設定時只提供嵌入的新手內容。`docs/baseline/wz-inventory.json` 保存 30,033 份 XML 的逐檔與整體雜湊；可用 `py -3 tools/inventory-wz.py --source-root <wz目錄> --output <manifest路徑>` 重建核對。載入資料不等於已支援該地圖的全部腳本與玩法。

## 開發與測試

```powershell
# 重建 Ent（Go tool directive 固定 generator 版本）
go generate ./ent

# 不需要外部服務的單元／協定測試
go test -mod=readonly ./...

# 已將 .local/environment.json 的屬性載入目前 Process environment 後：
$env:MAPLEGO_TEST_INTEGRATION = '1'
go test -mod=readonly -count=1 ./...
```

整合測試只在 Go 專用 DB 建立隨機測試帳號與 world 126，並清理自己的資料。覆蓋並行消費僅一次成功、重播拒絕、過期 session／epoch 拒絕、讀取失敗交易回復、保存／離線鎖順序與 Redis 回源。協定 fixture 以獨立編譯的原 Java AES／stats／look 做逐位元組對照，沒有呼叫運行中的 Java。

`tools/verify-isolation.ps1` 僅在 world 1 沒有活動 session 時，對經 PID／路徑／建立時間驗證的 Go world1 做 abrupt termination，檢查其他程序不重啟，再等租約到期正常恢復。其他世界與 Java 不會被指定為終止目標。

## 本里程碑限制

- 支援入門冒險家 job 0（創角附 1000 楓幣＋USE 欄 5×紅水 2000000）。進圖後 RecvMove＋距離 AOI 廣播；楓幣丟／撿走 PG ledger（operation_id 冪等，checkpoint 不得改 mesos）。紅水可使用並持久化，其他藥水、Buff／百分比回復、地圖藥水限制／冷卻的完整內容資料與背包物品丟撿尚未接通。斷線／錯開 checkpoint 持久化位置／進度。完整技能、戰鬥、怪物／NPC、任務、交易、倉庫與過圖（需 portal 內容）尚未接成玩法。
- 商城只提供獨立運行、health 與正確版本 handshake；購買及世界進出商城明確停用。換頻也回覆 blocked，沒有宣告已支援。
- stock v145 的交接使用隨機正整數 31-bit handle，PG 一次性消費並核對帳號、角色、channel、instance／epoch、期限及 loopback；實機已確認 client 會 echo handle。對外部署所需的高熵客戶端憑證、mTLS 與遠端配置還未開放。
- outbox 已寫入 PostgreSQL，但 worker 重播尚未實作；目前 roster 採立即刪除及 25 秒 TTL，展示可短暫落後，權威選角／創角仍檢查 PG。
- 目前角色資料結構與 persistence 為此垂直流程的子集。既有 160 表玩家資料尚未遷移；5,000 CCU、完整內容與備援 failover 尚未驗收。
- Native Windows 的 `-race` 尚未執行，因目前 CGO 未啟用；已執行實際 PG 並行交接／保存清理測試，不將其等同 race detector。

## 64 位元經驗值

預設開啟。後台設定「64 位元經驗值」（`long_experience`，環境變數 `MAPLEGO_LONG_EXPERIENCE`，設成 `0` 關閉）開啟時，EXP 上限改為 int64，角色資料、`UpdateStats` 的 EXP 與經驗值獲得訊息都改送 8 bytes。登入器由 `0x612` 的 bit2 得知；不認得 `0x612` 的舊登入器要在 `open.ini` 設 `[Features] LongExperience=1`，否則封包會錯位。開啟時 202 等以上的升級所需經驗照原本的表，不再壓在 20 億。關閉前要先把超過 int 的 EXP 降回去。詳見 Open-Maple 的 `docs/LONG_EXPERIENCE.md`。

## 楓幣與點數突破 21 億

預設開啟。後台設定「楓幣與點數突破 21 億」（`long_money`，環境變數 `MAPLEGO_LONG_MONEY`，設成 `0` 關閉）開啟時，楓幣、倉庫楓幣、Gash 與楓點的餘額上限改為 int64。進圖角色資料與 `UpdateStats` 的楓幣、倉庫餘額、商城錢包的 Gash 與楓點改送 8 bytes，登入器由 `0x612` 的 bit5 得知。

一次交易出價與一次倉庫存取也可以超過 21 億，客戶端送來的金額與服務器回給交易雙方的出價都改成 8 bytes。其餘單筆金額不變，仍在 21 億內：商品單價、一次拾取或丟出的楓幣。

關閉前要先把超過 2147483647 的餘額降回去（`game_characters.mesos` 與 `game_character_roster.snapshot`、`asset_storages.mesos`、`asset_cash_wallets`），否則該角色不會出現在角色清單，倉庫與商城錢包的封包也會帶著截斷的數字。詳見 Open-Maple 的 `docs/LONG_MONEY.md`。

## 公會人數突破 255

預設開啟。後台設定「公會人數突破 255」（`large_guild`，環境變數 `MAPLEGO_LARGE_GUILD`，設成 `0` 關閉，改了要重啟）開啟時，公會資料的人數、容量變更（GUILD_OPERATION 64）與公會聊天（MULTICHAT）的收件人數改用 2 bytes，登入器由 `0x612` 的 bit8 得知。

人數上限由兩個即時設定決定，存檔後幾秒內在線的公會成員就會收到新的公會資料，不用重啟或重登：

- 「公會人數上限」（`guild_max_capacity`，預設 512，最多 700）：每個公會最多幾人。公會本部 GP 擴充可以加到這個數字，楓幣擴充最多到 100。調低時超過的公會不會踢人，只是不能再收新人。700 是封包上限：建立公會聯盟時兩個公會的完整名單在同一個封包內。
- 「公會基本人數」（`guild_base_capacity`，預設 10）：每個公會不用擴充就有的人數，設成和上限一樣時所有公會直接開滿。

關閉 `large_guild` 時上限回到原版 200。詳見 Open-Maple 的 `docs/LARGE_GUILD.md`。

## 好友人數突破 255

預設開啟。後台設定「好友人數突破 255」（`large_buddy`，環境變數 `MAPLEGO_LARGE_BUDDY`，設成 `0` 關閉，改了要重啟）開啟時，好友名單的人數、好友上限變更（好友封包 mode 21）與進場角色資料裡的好友上限改用 2 bytes，登入器由 `0x612` 的 bit9 得知。

「好友人數上限」（`buddy_capacity`，預設 512，最多 1000，即時生效）是每個角色至少可以有的好友數；原版新角色是 232。存檔後幾秒內在線角色就會收到新的上限。調低時已有的好友不會被刪，只是不能再加。1000 是封包上限：完整好友名單（每人 61 bytes）在同一個封包內。關閉 `large_buddy` 時上限回到 255。

## 能力值與攻擊力超過 32767

預設開啟。`MAPLEGO_LONG_STATS=0` 關閉。開啟後 STR、DEX、INT、LUK 與 AP 在角色資料和 `UpdateStats` 改送 4 bytes，上限 9,999,999。客戶端 `open.ini` 的 `[Features] LongStats` 必須相同，否則封包會錯位。關閉前要先把超過 32767 的能力值與 AP 降回去。詳見 Open-Maple 的 `docs/LONG_STATS.md`。

## HP 與 MP 超過 99,999

預設開啟。後台設定「HP/MP 突破 99,999」（`long_vitals`，環境變數 `MAPLEGO_LONG_VITALS`，設成 `0` 關閉）開啟時，最大 HP 與 MP 的上限從 99,999 提高到 9,999,999：升級、轉職、任務、藥水、組隊 HP 都照新上限。HP、MP 在封包裡本來就是 4 bytes，格式不變；登入器由 `0x612` 的 bit7 得知，拿掉客戶端自己的 99,999 限制。migration `0088_long_vitals.sql` 把 `base_max_hp`、`base_max_mp` 的 CHECK 上限放寬到 9,999,999。

關閉時不用先改資料：基礎值超過 99,999 的角色照常讀取，實際上限回到 99,999。QA 指令 `!vitals <最大HP> <最大MP>` 設定基礎最大 HP、MP 並補滿。詳見 Open-Maple 的 `docs/LONG_VITALS.md`。

## 重登保留 Buff

預設開啟。只影響「從選角畫面重新登入」；換頻道、進出商城一律保留 Buff，跟開關無關。

- 開啟（預設）：技能 Buff 與消耗品 Buff 照剩餘時間恢復，召喚獸也會在身邊重新出現，存活到 Buff 結束為止。換頻道、進出商城也會帶回召喚獸。
- `MAPLEGO_RELOG_BUFFS=0`：重新登入時清掉角色存著的所有 Buff，跟原版一樣要重放。

剩餘時間照現實時間算，離線期間會繼續倒數；離線超過 Buff 剩餘時間，重登時就沒有了。只改伺服器，客戶端不用設定。

## 消耗品 Buff

USE 欄物品的 WZ `spec` 有 `time` 加上 `pad`、`mad`、`pdd`、`mdd`、`acc`、`eva`、`speed`、`jump` 時，使用後給對應的 Buff（可同時回 HP/MP）。Buff 來源 ID 是負的物品 ID（跟 Java 一樣），點圖示取消時客戶端會送回這個 ID。Buff 疊加（預設開啟，見下一節）關閉時，同屬性的 Buff 會互相覆蓋，跟技能相同。帶其他效果（變身、掉寶率、地圖限制等）的物品仍然拒絕使用。

## 同類型 Buff 疊加

做法與實測過程見 [STACK_BUFFS.md](STACK_BUFFS.md)。

預設開啟。後台設定「Buff 疊加」（`stack_buffs`，環境變數 `MAPLEGO_STACK_BUFFS`，設成 `0` 關閉）開啟時，不同技能或消耗品給的物攻、魔攻、命中、迴避、速度、跳躍會疊加，不再由後施放的覆蓋前一個。

- 做法：這幾種屬性改用客戶端原生的 indie 欄位（ANGEL_ATK、ANGEL_MATK、ANGEL_ACC、ANGEL_AVOID、ANGEL_SPEED、ANGEL_JUMP）送出。客戶端每個 indie 屬性保存一串「來源、數值、剩餘時間」，自己加總。
- 同一個來源重複使用（同一個技能再放、同一種藥水再吃）只刷新自己那一筆的時間，不會疊兩次。
- 取消或到期只移除那一個來源，其他來源保留；伺服器會重送剩下的清單。
- 物防、魔防還沒有驗證過的 indie 欄位，仍然互相覆蓋。
- 只改伺服器，客戶端不用設定。關閉時行為跟原本一樣。
- 資料表 `game_skill_buffs` 的主鍵改為 (character_id, stat, skill_id)（migration 0075），關閉時仍然一個屬性只存一筆。

## 登入器驗證服務器

預設關閉，本機測試不用設定。對外營運時用它讓登入器確認對面是持有私鑰的服務器，別人架的服務器或中間人無法讓登入器開關功能。線路格式與能擋住的情況見 Open-Maple 的 `docs/SECURE_CHANNEL.md`。

| 環境變數 | 內容 |
|---|---|
| `MAPLEGO_LAUNCHER_KEY` | 私鑰（base64）。登入、世界、商城三個服務都要設定同一把 |
| `MAPLEGO_LAUNCHER_KEY_FILE` | 改從檔案讀私鑰，`MAPLEGO_LAUNCHER_KEY` 沒設定時才使用 |
| `MAPLEGO_LAUNCHER_KEY_REQUIRED` | 設成 `1` 後拒絕沒有做金鑰交換的客戶端 |

步驟：

1. `go run ./cmd/launcherkey` 產生一組金鑰。私鑰只放在服務器的環境變數或檔案，不要進版本庫或資料庫。
2. 三個服務設定私鑰後重啟。健康檢查的 `launcher_key` 會顯示 `accepted`，這時新舊登入器都能連線。
3. 用公鑰建置登入器並發給玩家。
4. 玩家都更新後設定 `MAPLEGO_LAUNCHER_KEY_REQUIRED=1`，`launcher_key` 顯示 `required`。

私鑰格式錯誤時服務會直接啟動失敗，不會當成沒有設定。`MAPLEGO_LAUNCHER_KEY=<私鑰> go run ./cmd/launcherkey -public` 可以從私鑰印出公鑰。
