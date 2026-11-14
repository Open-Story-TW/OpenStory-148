-- Ardentmill's door to the Free Market. Legacy has no source for it; it does
-- what the town doors (pi.inFreeMarket) do: remember this map as the place the
-- Free Market exit returns to, then enter the market.
if portal:level() < 15 then
    portal:message("您必須是15級才能進入自由市場.")
    return
end
-- Legacy SavedLocationType.FREE_MARKET, as scripts/legacy/portal_driver.lua keeps it.
local saved = portal:exchange({ quest_data = { { id = 1900000000, data = tostring(portal:map_id()) } } })
if saved.ok then portal:warp(910000000, "st00") end
