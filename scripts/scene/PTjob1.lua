-- Phantom tutorial: back on the 水晶花園 (map 150000000, onUserEnter PTjob1)
-- the beginner becomes a level 10 幻影俠盜 and quest 25000 is finished. The
-- level, job, stats and quest are one server transaction
-- (internal/persistence/quest_jobs.go).
if scene:job() ~= 2003 or scene:quest_state(25000) ~= 1 then return end
if scene:advance_first_job(2400).ok then
    scene:say_as("計畫成功！真是漂亮的一仗。全新的楓之谷，歡迎怪盜幻影！", 0, false, false)
end
