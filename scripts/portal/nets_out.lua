-- legacy-portal-script
-- Adapted from Legacy scripts/portal/nets_out.js.
-- Original SHA256: f61c51ab32d673d878047f13c945d7c3b4a3a9319c2eba1530d180a8b4ba0c64.

function enter(pi)
    local returnMap
    returnMap = pi.getSavedLocation("MULUNG_TC")
    if (returnMap < 0) then
        returnMap = 260020500
    end
    pi.clearSavedLocation("MULUNG_TC")
    if (returnMap == 260020500) then
        pi.warp(returnMap, "piramid00")
    else
        pi.warp(returnMap, "unityPortal2")
    end
    do return true end
end
