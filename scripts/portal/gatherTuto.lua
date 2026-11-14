-- Source: Legacy scripts/portal/gatherTuto.js
-- Source SHA256: 054e05da8910f54387b78acc90de6b7f60446c875ebc4d8deb20b2c9f1812c9e
-- Ardentmill's side portal. A ticket opens one secret farm or mine, at most
-- three times a day per account for each place; the profession tutorial
-- quests open the two practice fields without a ticket.
local TICKETS = {
    { item = 4001480, map = 910001005 },
    { item = 4001481, map = 910001006 },
    { item = 4001482, map = 910001003 },
    { item = 4001483, map = 910001004 },
}
for _, ticket in ipairs(TICKETS) do
    if portal:item_count(ticket.item) > 0 then
        local used = portal:exchange({
            items = { { id = ticket.item, quantity = -1 } },
            logs = { { scope = "account", period = "day", name = "secret_field_" .. ticket.map, delta = 1, maximum = 3 } },
        })
        if used.ok then
            portal:warp(ticket.map, 0)
        else
            portal:message("這個地方一天最多只能進入三次。")
        end
        return
    end
end
if portal:quest_state(3197) == 1 or portal:quest_state(3198) == 1 then
    portal:warp(910001002, 0)
elseif portal:quest_state(3195) == 1 or portal:quest_state(3196) == 1 then
    portal:warp(910001001, 0)
else
    portal:message("您沒有任何入場券。")
end
