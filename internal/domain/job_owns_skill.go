package domain

// JobOwnsSkill is the v148 client's job/skill line test (0x6B5E94) for the
// cases it shares with every job: a skill of book b (skill/10000) belongs to
// job j when b is a job group (b%100 == 0) of the same family (b/100 == j/100),
// or when j is in b's line at or above b's advancement (j/10 == b/10 and
// j%10 >= b%10). Book 8000 belongs to everyone. The client's Evan and Jett
// special cases are not needed by the Shadow Partner check that uses it.
func JobOwnsSkill(job, skillID int) bool {
	book := skillID / 10000
	if book == 8000 {
		return true
	}
	if book%100 != 0 {
		return book/10 == job/10 && job%10 >= book%10
	}
	return book/100 == job/100
}
