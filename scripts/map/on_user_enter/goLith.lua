-- Source: Legacy MapScriptMethods.goLith and scripts/map/onUserEnter/goLith.js.
-- JS SHA256: 3760377c960d42184a7c64f8c9947fc5c490a11a79b0412261e2b057691f2213
-- The mounted WZ scene owns the five-second transfer to Victoria Harbor.
local gender = map:gender()
map:play_scene("Effect/Direction3.img/goLith/Scene" .. gender)
