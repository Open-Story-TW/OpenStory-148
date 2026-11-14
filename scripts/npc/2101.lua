-- Source: Legacy scripts/npc/2101.js
-- Source SHA256: 340e7517c3aaee1d511438cf449ccac9186ea4364b35650bb36fcce2963dd18a
-- The original target map 3 is intentionally preserved. Missing content is a
-- host error; this conversion does not pretend that map 3 has been supplied.
local completed = npc:ask_yes_no("Are you done with your training? If you wish, I will send you out from this training camp.")
if not completed then
    npc:say("Haven't you finish the training program yet? If you want to leave this place, please do not hesitate to tell me.")
    return
end
npc:say("Then, I will send you out from here. Good job.")
npc:warp(3, 0)
