# Sequential Lua runtime

The runtime pins [`github.com/arnodel/golua v0.4.0`](https://github.com/arnodel/golua/releases/tag/v0.4.0), a pure-Go **Lua 5.5** implementation. This is not Lua 5.4 or GopherLua's Lua 5.1. GoLua supplies coroutine suspension and CPU/allocation accounting; see its [quota contract](https://github.com/arnodel/golua/blob/v0.4.0/quotas.md). Its allocation limit is VM accounting, not a promise about the whole Go process's RSS.

Each invocation gets a separate VM, a source SHA256 snapshot, an authenticated `Identity`, and one serialized coroutine. The source executes at top level:

```lua
local prior = npc:log("character", "day", "daily-gift")
if prior == 0 and npc:ask_yes_no("領取今日獎勵？") then
    npc:exchange({
        items = {{ id = 2000000, quantity = 1 }},
        logs = {{ scope = "character", period = "day", name = "daily-gift",
                  delta = 1, expected = 0, maximum = 1 }},
    })
    npc:say("已領取。")
end
```

`say`, `ask_yes_no`, and `menu` suspend without holding any database transaction. A matching `PromptID`, session ID, character ID and generation are required before continuation. IDs are single-use. The native v145 NPC response does not itself contain the engine prompt ID; the network adapter must preserve that distinction and must not claim universal duplicate-packet detection merely by substituting the current prompt ID.

`say(text, allowBack?, allowNext?, speaker?)` returns `true` for Next/Finish or `false` for Back. Setting `allowNext=false` shows the native Finish button; Finish completes the current page and resumes the script. The engine validates the offered navigation. Back is an ordinary script branch, not VM rewind or rollback of already committed rewards. `menu(text, {{id=...,text=...},...})` returns an offered ID; `ask_yes_no` returns a Boolean. Escape/disconnect is handled by cancelling the session. `speaker` is the Legacy talk flag byte (0-3): 1 hides the close button, 2 draws the player as the speaker.

```lua
local pages = {"第一頁", "第二頁", "第三頁"}
local page = 1
while page <= #pages do
    local forward = npc:say(pages[page], page > 1, page < #pages)
    if forward then page = page + 1
    else page = page - 1 end
end
```

The native wire distinguishes Next/Finish (`mode=1`), Previous (`mode=0`) and Cancel (`mode=255`). Cancellation ends the invocation even when Previous is offered; it never returns `false` into the Lua branch. Keep reward commits after the navigation loop or protect them with durable quest/log conditions.

## Host operations

`Host.Execute(ctx, Identity, Command)` is the only bridge to the server. No Go pointer, Java object, raw SQL or arbitrary module loader is exposed to Lua. The host validates current ownership, NPC interaction context, item rules and permissions again for each command. Commands have generated operation IDs; the host must use them for durable idempotency.

- `map_id`, `level`, `job`, `hp`, `quest_state`: typed queries; unsupported capabilities return errors.
- `advance_warrior`: reviewed first-job transition bound to NPC 1022000 in map 102000003, for a living level-10-or-higher beginner. Stat reset, AP conservation, SP, health bonus, equipment/ETC capacity growth and weapon grant commit together. This is not an arbitrary job setter. The later custom-quest advancement branches in the legacy NPC remain unconverted.
- `quest_start`, `quest_complete`, `quest_forfeit`: a quest ID, server-side WZ requirements and a single atomic quest/reward transaction. `Command.Script` always contains the pinned source name assigned by the engine, not a value supplied by Lua.
- `quest_data(id)`: durable custom quest/dialogue data, independent of started/completed state and monster counters. Missing data reads as an empty string. Include `quest_data = {{id=700001, data="rewarded", expected=""}}` in `exchange` to advance that record atomically with a reward. Values are bounded to 4 KiB and remain private to the authenticated character.
- `open_storage`, `open_shop`, `warp`: typed interaction commands whose authorization belongs to the host. Both `warp(mapID, portalID)` and `warp(mapID, "portalName")` resolve an existing destination from WZ; names are exact and never fall back to a different spawn.
- `ask_avatar(text, styles)` and `change_look(coupon, "hair"|"face", style|{styles})`: Legacy `cm.askAvatar` (ASK_AVATAR, message type 9) and `cm.setAvatar`/`cm.setRandomAvatar` for beauty salons. The host drops hair/face IDs missing from `Character.wz` before the preview, `ask_avatar` returns the chosen ID, and `change_look` uses one 515xxxx coupon from the cash tab and sets the look in one exchange (a list means a server draw). It returns `{ok=true, style=id}` or `{ok=false, reason="no_coupon"}`. `gender()`, `hair()` and `face()` read the current look.
- `ask_map_selection({{id=..., text=...}, ...})`: Legacy `cm.askMapSelection` (message type 0x10), the client's slide menu used by the Dimensional Mirror 9010022. The server sends Legacy's `#id#label` list with its two window flags and returns the chosen id; an id the window did not offer is refused. Labels cannot contain `#`.
- `read_log`: `npc:log(scope, period, name)`, with explicit character/account/world and permanent/hour/day/week/month/year periods.
- `exchange`: one `domain.ScriptExchange` descriptor containing item/meso/log and custom quest-data changes. The host supplies the authenticated session and operation ID, then commits all changes together. A read followed by a grant is not itself an atomic limit check; use expected/maximum conditions in the same exchange.
- `open_parcel()`: opens the 宅配 parcel window (Duey, NPC 9010009); sending, taking and deleting parcels are then window requests the world server checks, not script commands.

Existing `getLogCount` semantics must be mapped per source: types 1/3 use rolling 24 hours, 5/6 use rolling 7 days, 2/4 are permanent, and 7/8 use mutable counters with separate calendar expiry rules. Those meanings are not silently translated to calendar day/week periods.

## Debugging and resource bounds

`StartOptions.Debug` pauses before the first executable statement. `Breakpoints` are original source line numbers. `Step` pauses at the next statement, including statements inside called Lua functions. `RequestPause` targets only this invocation. `Continue` resumes it; `Inspect` returns a bounded deep copy of in-scope locals and user globals, excluding runtime globals. `Source` returns this invocation's pinned source, including after a later registration replaces that name. There is no arbitrary evaluation or setter endpoint. Arming the next NPC/portal invocation is tied to its current login generation; it cannot carry into a reconnect. Mutating host commands revalidate object, map, channel and interaction distance after a pause.

GoLua exposes line hooks but does not expose local registers through `debug.getlocal`. The compiler therefore inserts private AST trace calls that capture lexical names, preserving original source locations. The private hook is captured as a local before source execution and its identifier is reserved. Comments/blank lines have no executable breakpoint. Tables are summarized with depth/entry/string limits; functions and VM internals are represented as opaque types.

The VM enforces cumulative CPU and allocation budgets. Statement checkpoints enforce per-resume CPU/time and cancellation checks; each host call has a separate two-second context. A single runtime context persists across coroutine yields so frame allocations are charged consistently. We do not push/pop memory contexts around each resume, which would wrongly transfer live frame accounting between scopes.

Default limits: 64 KiB source, 16 MiB accounted allocation, 10 million cumulative CPU ticks, 100,000 ticks per resume, 250 ms per statement-driven run, 512 prompts, 256 host operations, 3-minute prompt reply validity and 30-minute session lifetime. Host operations must respect their contexts. Standard filesystem, OS, network, package loading, debugger mutation and raw Go bridge functions are unavailable.

A GM character sees a `[GM NPC]` chat line for each step of an NPC conversation: the NPC clicked, the script started or why none ran (no `npc/<id>.lua`, out of reach, trading), each prompt with its source line, each answer, every non-query host command and its outcome, and how the conversation ended. Ordinary players never receive these lines.

## Recovery and events

The runtime does not serialize Lua stacks and does not impose one universal disconnect recovery policy. Scripts decide their restart/resume behavior through authoritative quest/log state. A reconnect creates a new invocation, and durable host commands still need business-level conditions to avoid granting a reward twice across different invocations.

### Event instances

Party quests and boss fights run as event instances (`internal/world/events.go`), not as Lua event scripts. `internal/content/events.go` declares each event: its stage maps, entry portal, exit map, time limit, party size, level range and `Keep`, the member count below which it ends. This replaces the per-event Legacy `event/<name>.js`, whose `setup`/`changedMap`/`playerDisconnected`/`leftParty`/`scheduledTimeout` bodies differ only in those values.

The stage maps are the channel's ordinary maps, so one group holds an event per channel, as Legacy's `state` property did. Starting it puts every monster of the stage maps back (including WZ `mobTime -1` monsters, which otherwise stay dead once defeated), moves the leader's party members who stand on the leader's map into the first stage and shows the countdown clock. A member leaves the group by arriving on any other map, disconnecting or leaving the party. The instance ends at its time limit, when fewer than `Keep` members remain, or when a script finishes it; everyone still on a stage map then goes to the exit map. Someone who is on a stage map without belonging to the running group (a login after a restart, for example) is sent to the exit map too, unless they are a GM.

Instances are memory only: a world restart ends them, and nothing a script stored in one is kept. Rewards a stage NPC gives must therefore be ordinary `exchange` commits.

NPC sources reach an instance through:

- `npc:event_status(name)` returns `{known, busy, members, inside}` for this channel and an `attempt` identifier for members to key their one-time rewards.
- `npc:event_start(name)` checks eligibility and moves the group. It returns `{ok, reason, here, qualified}`. A refusal has reason `unknown`, `no_party`, `not_leader`, `party_size`, `level`, `busy` or `unavailable`.
- `npc:event_get(key)` / `npc:event_set(key, value)`: text properties of the caller's group (stage progress); `event_get` reads `""` outside a group, `event_set` is refused there.
- `npc:event_effect("clear" | "wrong")`: the party quest result animation and sound for everyone on the map.
- `npc:event_areas()` returns a bitmask of the map's WZ areas occupied by living event members or their dropped items, with bit 0 for the first listed area.
- `npc:event_party_exp(amount)` grants stage experience once per attempt, map and active member. The party leader can retry the call after a failure.
- `npc:event_finish()`: the party leader ends the event; `npc:event_leave()`: the caller alone goes to the exit map.

`npc/9020000.lua` starts the Kerning party quest. `npc/9020001.lua` handles the five stages and bonus entry. The four `party_portal` entrances check the preceding stage's clear flag on the server. The event definition spawns King Slime when the maps reset.

The first-job path for NPC 1022000 is a further partial migration: the top-level Lua checks job/level, retains Back navigation and explicit confirmation, then executes one bounded advancement command. Reopening it after that commit sees job 100 and does not repeat the weapon, slots or health grant. Its second/third-job custom-quest branches are still pending; this partial source is not counted as a completed whole-file migration below.

The `scripts` package embeds reviewed source independently of the working directory. Three branching legacy NPCs (2100, 2101, 2007), 68 exact unconditional service wrappers (28 storage and 40 shops), 35 literal NPC wrappers (26 fixed messages and 9 numeric warps), 106 unconditional portal warps (95 numeric and 11 named destinations), and quest 1021 (Roger's Apple) are converted, with original source hashes and branch/command tests. `scripts/service-npcs.json`, `scripts/simple-npcs.json` and `scripts/portal-migrations.json` record these migrations. `tools/migrate-service-scripts.py --include-portals` and `tools/migrate-simple-npcs.py` only accept complete known shapes and leave other scripts for review. Literal message conversion tokenizes quoted data separately, preserves text/escapes, and rejects dynamic expressions or additional host calls. Portal invocations use `portal:warp(map, portal)` and the same debugger, with a distinct host that validates source position. The conditional `entertraining.lua` additionally permits narrowly authenticated quest-state reads for 1041–1044 and the original nonmodal denial message; every command revalidates the actual source portal, and warping rechecks the currently eligible branch. Its separate `scripts/conditional-portals.json` manifest preserves the unconditional migration inventory.

Five reviewed first-job NPCs use `npc:advance_first_job(job)` with source-specific eligibility and one atomic reward transaction. `scripts/first-jobs.json` records exact source hashes, job choices and pending MCP cases. These branches retain differences in legacy stat-reset order, bag expansion and ammunition stacks; they do not expose an arbitrary job setter or imply later advancements and class combat are complete. Together with the conditional portal the current package has 219 Lua sources.

Quest 1021 commits acceptance, apple grant and HP reduction before subsequent informational dialogue, so reconnecting reads the started quest instead of repeating an unrecorded grant. This is an intentional transaction/recovery boundary; the old Java callback saved acceptance later. The original destinations in literal portal scripts are retained and may fail the host's content availability check. This is not completion of the 3,128-script migration or P5; runtime conversion tests do not stand in for native MCP acceptance of these scripts.

Quest persistence currently supports server-authoritative start/complete/forfeit, character-scoped durable state, ordered mob counters, level/job/fame/map/date/quest/item/mob requirements, and item/meso/EXP actions. Positive item `prop` values form one weighted choice pool after gender and Java job-family filtering; absent `prop` or `-2` grants an unconditional reward. The draw uses server entropy, and the committed receipt restores the chosen inventory without rerolling. Quest 1035 now has persistence coverage for its single random hat, required-item consumption, full-bag rollback and replay. Equipment grants use WZ base attributes and actual bag capacity. Completion-time player choices (`prop=-1`) require a valid zero-based index among eligible entries, separate from the random pool. The selected index is stored in the receipt, and retries cannot change it. Start-time choices, expiring equipment and unknown fields remain rejected. Full native cycles are covered for 1031 and the reviewed scripted 1021; 1035 and player-choice rewards still need native MCP completion. Blocked or expired legacy entries stay blocked. Inventory changes, character growth, quest state and command receipts commit together; no transaction remains open during a Lua prompt. The initial character packet restores started/completed quests after reconnect, with the original Legacy compressed timestamp format for completed snapshot entries.

## Generated legacy quest state machines

`tools/migrate-quest-scripts.mjs` converts Legacy `scripts/quest/<id>.js` sources whose JavaScript and `qm` calls fall inside a fixed subset. The output keeps the legacy `start`/`end` state machine statement for statement (`end` becomes `finish`) and opens with the `-- legacy-quest-state-machine` marker. `scripts.Source` appends `scripts/legacy/quest_driver.lua` to such a file; the driver supplies `qm`, calls `finish` for a started quest and `start` otherwise, and feeds each answer back as the Legacy `(mode, type, selection)`. `scripts/legacy-quests.json` records every generated source with both hashes. English dialogue is replaced through `tools/quest-zh.json` (whole string literal -> Traditional Chinese) at generation time. A refusal the script sends back to an earlier line ends the conversation the second time, so a source without its own exit cannot loop. These conversions are mechanical: they are covered by a simulation test that drives every source through both phases, not by a per-script review or native acceptance.

One legacy call is one transaction. The driver collects `gainItem`, `gainExp`, `gainMeso`, `forceStartQuest`, `forceCompleteQuest` and `setQuestCustomData` during a call and commits them as a single `exchange` when the call returns, before the prompt of that call is shown. A refused exchange (full bag, missing item) ends the dialogue with a notice and changes nothing, where Legacy silently skipped the item and still completed the quest.

`exchange` accepts two fields for this, from `quest/` sources only: `exp = n` and `quests = {{id=, state=1|2, data=?}}`, the Legacy forced status change without WZ requirement checks or WZ rewards. Because such a source grants its own rewards, the world launches it only after the WZ check for that phase passes (`MapleQuest.canStart` for a start script on an unstarted quest, `canComplete` for an end script on a started one). A quest whose WZ requirements contain a field the server does not parse stays closed; a GM sees the reason in the quest click notice. `npc:can_hold(id, quantity)` and `npc:ask_accept(text)` (Legacy ACCEPT_DECLINE, message type 0x0E) were added for these sources. Forced-start custom data is sent to the client when it is written, and the login quest snapshot shows it for a started quest without monster counters.

Quest requirements read that custom data too. `infoNumber`/`infoex` demand that the record of the named quest (the quest itself when `infoNumber` is absent) satisfy one entry: plain entries compare the text, `cond = 1` is a numeric minimum, and `exVariable` selects one key of a `key=value;key=value` record. Legacy never loaded these fields and left them to the client; here the server checks them as well, so a quest stays closed until something writes the record. `cond = 1` as a minimum is inferred from the WZ values (counts and thresholds), and other `cond` values stay unsupported. `interval` is the minutes after completion before a repeatable quest may start again. Records are loaded into `Character.QuestRecords`.

## Beginner quest lines

`subJobFlags` is a mask with one bit per Explorer subcategory (1 ordinary, 2 Dual Blade, 4 Cannoneer, 1024 Jett) checked against the character's `subcategory`; Legacy read it as `flags/2 == subcategory`, which agrees on 1/2/4 only. A `quest` prerequisite without `state` means "not started": the Cygnus and Resistance path quests list their sibling paths that way so only one can be taken.

The driver also accepts the Legacy calls the family tutorials use: `sendNextS`/`sendNextPrevS`/`sendOkS`/`PlayerToNpc`/`sendPlayerToNpc` show a say page with the talk flag (bits 1 and 2 of the Legacy type), `isQuestCompleted` and `removeAll` read and change state like their siblings, and the presentation-only calls (`summonMsg`, `playerSummonHint`, `AranTutInstructionalBubble`, `evanTutorial`, `showWZEffect`, `MovieClipIntroUI`) show nothing. Generated tutorials: Cygnus 20000, 20008, 20010-20012, 20015, 20100; Aran 21000-21018, 21100; Evan 22000-22007, 22100.

Tutorials whose Legacy decline branch stepped back a line and looped until ESC (20001, 20002, 20013, 20016, 20017) and the first-job quests are reviewed sources instead. The job quests (Cannoneer 2570, Cygnus 20101-20105, Aran 21101, Resistance 23011-23013) call `npc:advance_first_job(job)`; `internal/persistence/quest_jobs.go` binds each to its source, NPC, beginner job and quest state, and commits the job, stat reset, SP, health gain, bag growth, items, skills and quest completion in one transaction. Legacy's changeJob gain table leaves out 501 and 1200-1500; they take the gain of the Explorer class they mirror.

Quest 7707 (凌晨重置任務) is a v148 client system quest the v145 Quest.wz lacks. The client starts it by itself once a day wherever the character is; the server records it as finished for the day (TMS time) without an NPC.

A `normalAutoStart` quest without a start script is offered by the client wherever the character stands, so its start transition does not need the NPC on the map; every other start requirement still applies. A generated start script of such a quest (the medal quests of NPC 9000040) also runs away from the NPC, bound to the map the character stands on; hand-written start scripts and every completion script still run at the NPC.

### Dual Blade (v148 line)

The v148 client marks the old Dual Blade line (2350-2386) `blocked` and offers a new one that the v145 Quest.wz lacks. `internal/content/quests_v148.json` carries the v148 Check/Act/QuestInfo entries (without dialogue) for its job quests only, and is read when the data root has no entry for a quest:

| Quest | Start | End | Job |
|---|---|---|---|
| 2622 雙刀流 | auto, Lv20 thief with subcategory 1 | Lady Syl 1056000 | |
| 2623 下忍 | `quest/2623.lua` after 2622 | `quest/2623.lua`, 1 × 4032616 | 400 → 430 |
| 2637 影武者之路1 | auto, Lv30 job 430 | Lady Syl | |
| 2638 影武者之路2 | `quest/2638.lua` after 2637 | `quest/2638.lua`, 30 × 4031013 | 430 → 431 |
| 2641 上忍之路1 | auto, Lv55 job 431 | Lady Syl | |
| 2642 上忍之路2 | after 2641 | `quest/2642.lua`, 99 × mob 6230600 | 431 → 432 |

Legacy has no scripts for these, so the three sources are reviewed Chinese lines taken from the v148 quest text. Each advancement checks the quest's own WZ completion demand inside the transaction, takes the items it names and follows the rewards of Legacy's older line: q2363e (katara 1342000 and four slots in every bag), q2369e (nothing) and q2374e (1132021). A second job keeps the player's stats; Legacy's 430 HP gain of 200-250 plus 150-200 applies, and 431/432 gain nothing, as in Legacy's table.

The story quests of the new line (2387, 2600-2621, 2624-2636, 2639, 2640, 2643, 2645) are not supplied: they need v148 monsters and NPCs (9300521-9300528, 1058001) the v145 content lacks, start scripts from a remote NPC, and buff demands.

## Generated Legacy portal scripts

`tools/migrate-portal-scripts.mjs` converts Legacy `scripts/portal/<name>.js` sources whose JavaScript and `pi` calls fall inside a fixed subset (UTF-8 or EUC-KR files; a `try` keeps its body and drops the handler). The output opens with `-- legacy-portal-script`; `scripts.Source` appends `scripts/legacy/portal_driver.lua`, which supplies `pi`, runs `enter(pi)` once, commits its item/mesos/EXP/forced-quest/saved-location effects as one `exchange`, opens a `pi.openNpc` conversation, and performs the last warp. Reviewed portal sources are never overwritten. `scripts/legacy-portals.json` lists every generated source with both hashes and every Legacy script still pending with the first construct the converter refused (event managers, parties, reactors, info-quest tutorials and so on).

Saved locations (`pi.saveLocation("FREE_MARKET")` and the other Legacy `SavedLocationType` names) are kept as the character's server-side script record `1900000000 + index`. `pi.getMap()` monster and player counts use the `monster_count` and `player_count` portal queries; `playPortalSE`, effects, instructions and UI locks show nothing. The portal host still revalidates the source portal for every command, and only generated sources may read the character, exchange or open an NPC.

A GM character sees a `[GM Portal] <map>/<portal>「<script>」` chat line for each portal invocation: the script started, every non-query command with its outcome and the converted Legacy line it came from, how it ended, and, when nothing ran, whether Legacy has the script and why it is not migrated yet. Ordinary players never receive these lines.

## Cutscenes

`scripts/scene/<name>.lua` is a conversation that can also direct the player's character. The world starts one from a map's `onUserEnter` name or from a portal's script name, and only for the names and maps listed in `internal/world/scenes.go`. The script global is `scene`; it has every NPC command plus:

| Call | Effect |
|---|---|
| `scene:direct("lock", 1 or 0)` | take or return the controls (the client shows black bars); returned by itself when the script ends or fails |
| `scene:direct("alone", 1 or 0)` | hide the other players |
| `scene:direct("walk", 0, 1 or 2)` | stop, walk left, walk right |
| `scene:direct("action", id, ms)` | play a character action |
| `scene:direct("effect", path, ms, x, y)` | an Effect.wz animation at an offset from the character |
| `scene:direct("screen", name)`, `"sound"`, `"music"` | a Map.wz/Effect.img animation over the view, a sound, new background music |
| `scene:wait(ms)` | continue when the client reports the time has passed |
| `scene:say_as(text, speaker[, back[, next]])` | a line spoken by the player (speaker 0) or by the NPC template given |
| `scene:show_images{path, ...}` | picture pages in one window (the client's tutorial pages) |

The walk, action, effect and wait calls only work while the controls are locked. The opcodes and layouts are in `internal/protocol/scene_packets.go`, each with the client handler it was read from.

### Phantom tutorial

The v148 client creates a Phantom as beginner job 2003 on map 915000000. The maps, the starter outfit and the guards are not in the v145 data root; `internal/content/v148` carries them and is read for any file the root lacks.

| Map | Source | What happens |
|---|---|---|
| 915000000 臥室 | `scene/PTtutor000.lua` | Phantom paces and thinks aloud, then walks out |
| 915000100 大廳 | `quest/25000.lua`, `portal/PTtutor100_0.lua` | 阿弗雷德 starts quest 25000; the exit opens after that |
| 915000200 外部 | `scene/PTtutor200_0.lua` | one line, then the Knights' Hall |
| 915000300 騎士之殿 | `scene/PTtutor300.lua`, `PTtutor300_0.lua`, `PTtutor300_2.lua` | the client's picture pages; either checkpoint leads to the disguise map |
| 915000301 騎士之殿 | `scene/PTtutor300_2.lua` | 奇丹 stops the disguised Phantom once (the map itself dresses the character) |
| 915000400 耶雷弗 | `scene/PTtutor400_0.lua` | one line on the way |
| 915000500 大會議場 | `scene/PTtutor500.lua` | the hearing of 希拉 and 西格諾斯, then the ship |
| 150000000 水晶花園 | `scene/PTjob1.lua` | level 10, job 2400, the beginner skills and quest 25000 in one transaction |

Not done: the guards of the Knights' Hall (9300498, 9300507) with the two tutorial skills 20031211 and 20031212, the NPCs of 希拉's entrance, and the two movies. Legacy has no Phantom tutorial; the lines were written in Chinese for this server.
