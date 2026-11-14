-- Source: Legacy scripts/portal/entertraining.js
-- Source SHA256: 7e9ef61deb880a99a23f9e294969d35c2c5754444b8ddb3940abd69d56ab91cc
-- Preserve the first active training quest's destination and portal4.
if portal:quest_state(1041) == 1 then
    portal:warp(1010100, 4)
elseif portal:quest_state(1042) == 1 then
    portal:warp(1010200, 4)
elseif portal:quest_state(1043) == 1 then
    portal:warp(1010300, 4)
elseif portal:quest_state(1044) == 1 then
    portal:warp(1010400, 4)
else
    portal:message("Only the adventurers that have been trained by Mai may enter.")
end
