package domain

// Job families follow Legacy GameConstants and the v145 client's own checks.
// The client decides the SP wire format itself (0x4B98D1), so ExtendedSPJob
// must match it exactly even where Legacy's isSeparatedSp differs.

// Native creation races sent in RecvCreate (client 0x5A1DCA race buttons).
const (
	RaceResistance = 0
	RaceExplorer   = 1
	RaceCygnus     = 2
	RaceAran       = 3
	RaceEvan       = 4
	RaceMercedes   = 5
	RaceDemon      = 6
	RacePhantom    = 7
)

// PhantomStarterCape is the one cape of v148 MakeCharInfo 2003.
const PhantomStarterCape = 1102347

// Explorer sub jobs (character subcategory).
const (
	SubJobNone      = 0
	SubJobDualBlade = 1
	SubJobCannoneer = 2
)

// SPBookCount is the Legacy remainingSp array length.
const SPBookCount = 10

type SPBookTable [SPBookCount]int

// CreationRace is one character-creation route. Family names the
// Etc.wz/MakeCharInfo.img node whose item lists the client sends.
type CreationRace struct {
	Race   int
	SubJob int
	Job    int
	MapID  int
	Family int
}

// creationRaces mirrors Legacy LoginInformationProvider.JobType, including
// its custom start maps. The v145 client never sends Mercedes(5)/Demon(6)
// (0x591622 returns before COutPacket), so they are not creatable here.
// Phantom (7) is new in v148: beginner job 2003 on the airship of the
// client's own tutorial (Map.wz 915000000, quest 25000 asks for job 2003).
// Cannoneer (1/2) is absent from Legacy's enum; saveNewCharToDB's
// subcategory==2 branch gives map 3000600.
var creationRaces = []CreationRace{
	{Race: RaceResistance, Job: 3000, MapID: 931000000, Family: 3000},
	{Race: RaceExplorer, Job: 0, MapID: 10000, Family: 0},
	{Race: RaceExplorer, SubJob: SubJobDualBlade, Job: 0, MapID: 10000, Family: 0},
	{Race: RaceExplorer, SubJob: SubJobCannoneer, Job: 0, MapID: 3000600, Family: 0},
	{Race: RaceCygnus, Job: 1000, MapID: 130030000, Family: 1000},
	{Race: RaceAran, Job: 2000, MapID: 914000000, Family: 2000},
	{Race: RaceEvan, Job: 2001, MapID: 900090004, Family: 2001},
	{Race: RacePhantom, Job: 2003, MapID: 915000000, Family: 2003},
}

// CreationGenderFree reports the families whose creation screen offers both
// genders whatever the account's own is (MakeCharInfo info/choosableGender).
func CreationGenderFree(family int) bool { return family == 2003 }

func CreationRaceFor(race, subJob int) (CreationRace, bool) {
	for _, r := range creationRaces {
		if r.Race == race && r.SubJob == subJob {
			return r, true
		}
	}
	return CreationRace{}, false
}

// CreationFamily resolves a validated creation request's family/sub job.
func CreationFamily(family, subJob int) (CreationRace, bool) {
	for _, r := range creationRaces {
		if r.Family == family && r.SubJob == subJob {
			return r, true
		}
	}
	return CreationRace{}, false
}

func IsExplorerJob(job int) bool   { return job >= 0 && job < 600 } // no 6xx-9xx Explorer branch
func IsCygnusJob(job int) bool     { return job >= 1000 && job < 2000 }
func IsAranJob(job int) bool       { return job >= 2000 && job <= 2112 && job != 2001 && job != 2002 }
func IsEvanJob(job int) bool       { return job == 2001 || job >= 2200 && job <= 2218 }
func IsMercedesJob(job int) bool   { return job == 2002 || job >= 2300 && job <= 2312 }
func IsPhantomJob(job int) bool    { return job == 2003 || job/100 == 24 }
func IsResistanceJob(job int) bool { return job >= 3000 && job <= 3512 }
func IsDemonJob(job int) bool      { return job == 3001 || job >= 3100 && job <= 3112 }
func IsCannoneerJob(job int) bool  { return job == 1 || job == 501 || job >= 530 && job <= 532 }
func IsMihileJob(job int) bool     { return job == 5000 || job == 5100 || job >= 5110 && job <= 5112 }
func isZenJob(job int) bool        { return job == 508 || job >= 570 && job <= 572 }
func isPirateRevampJob(job int) bool {
	return job == 509 || job >= 580 && job <= 582 || job >= 590 && job <= 592
}

// IsBeginnerJob covers every family's level-1 job.
func IsBeginnerJob(job int) bool {
	switch job {
	case 0, 1, 1000, 2000, 2001, 2002, 2003, 3000, 3001, 5000:
		return true
	}
	return false
}

// ExtendedSPJob is the client's per-book SP predicate (v148 0x4C76BA):
// Resistance/Demon (job/1000==3), Evan (2001, 22xx), Mercedes (2002, 23xx),
// Phantom (2003, 24xx) and the 5000/51xx family the v148 client added.
func ExtendedSPJob(job int) bool {
	return job/1000 == 3 || job == 2001 || job/100 == 22 || job == 2002 || job/100 == 23 || job == 2003 || job/100 == 24 || job == 5000 || job/100 == 51
}

// SkillBook mirrors Legacy GameConstants.getSkillBook(job).
func SkillBook(job int) int {
	if job >= 2210 && job <= 2218 {
		return job - 2209
	}
	switch job {
	case 2310, 2410, 3110, 3210, 3310, 3510, 5110:
		return 1
	case 2311, 2411, 3111, 3211, 3311, 3511, 5111:
		return 2
	case 2312, 2412, 3112, 3212, 3312, 3512, 5112:
		return 3
	}
	return 0
}

// SPTable is the per-book SP the client sees. The live pool c.SP always
// belongs to the current job's book; SPBooks parks the other books.
func (c Character) SPTable() SPBookTable {
	table := c.SPBooks
	table[SkillBook(c.Job)] = c.SP
	return table
}

// SetJob changes job and, like Legacy remainingSp[getSkillBook(job)], leaves
// unspent SP in the old book while the new book's SP becomes the live pool.
func (c *Character) SetJob(job int) {
	from, to := SkillBook(c.Job), SkillBook(job)
	if ExtendedSPJob(job) && from != to {
		// A book is a byte on the wire (sp_books CHECK 0..255); QA levels can
		// leave more in the current book, which cannot be shown or parked.
		c.SPBooks[from] = min(max(c.SP, 0), 255)
		c.SP = c.SPBooks[to]
		c.SPBooks[to] = 0
	}
	c.Job = job
}

// CanLearnSkill ports Legacy Skill.canBeLearnedBy: beginner skills belong to
// one family, other skills to one branch and an advancement tier at or below
// the job's (as the Adventurer-only CanLearnAdventurer already required).
func CanLearnSkill(job, skillID int) bool {
	owner := skillID / 10000
	switch owner {
	case 0:
		return IsExplorerJob(job)
	case 1:
		return IsCannoneerJob(job)
	case 1000:
		return IsCygnusJob(job)
	case 2000:
		return IsAranJob(job)
	case 2001:
		return IsEvanJob(job)
	case 2002:
		return IsMercedesJob(job)
	case 2003:
		return IsPhantomJob(job)
	case 3000:
		return IsResistanceJob(job)
	case 3001:
		return IsDemonJob(job)
	case 5000:
		return IsMihileJob(job)
	case 8000, 9000, 9200, 9201, 9202, 9203, 9204:
		// Shared character skills (monster riding 80001000, the 9000 set and
		// professions): Legacy grants them to every job outside SP books.
		// 9100 guild skills belong to the guild, not a character.
		return ValidJob(job)
	}
	// TMS v148 has three additional pirate branches. The first-tier books
	// 508 and 509 are inherited only by their original branches.
	if isZenJob(owner) != isZenJob(job) || isPirateRevampJob(owner) != isPirateRevampJob(job) {
		return false
	}
	if job < 0 || job/100 != owner/100 || job/1000 != owner/1000 {
		return false
	}
	for _, sameFamily := range []func(int) bool{IsPhantomJob, IsCannoneerJob, IsDemonJob, IsExplorerJob, IsCygnusJob, IsAranJob, IsEvanJob, IsMercedesJob, IsResistanceJob, IsMihileJob} {
		if sameFamily(owner) && !sameFamily(job) {
			return false
		}
	}
	branch, ownerBranch := job/10%10, owner/10%10
	if branch == 0 && ownerBranch > branch {
		return false
	}
	if ownerBranch != 0 && ownerBranch != branch {
		return false
	}
	// Legacy compares owner/10 > job/10 here, which lets a 2nd job (110)
	// learn its 3rd job's skills (111); the tier must also be reached.
	return owner <= job
}

// validJobs lists supported playable job books in Skill.wz.
var validJobs = map[int]bool{}

func init() {
	for _, band := range [][2]int{
		{0, 0}, {100, 100}, {110, 112}, {120, 122}, {130, 132}, {200, 200}, {210, 212}, {220, 222}, {230, 232},
		{300, 300}, {310, 312}, {320, 322}, {400, 400}, {410, 412}, {420, 422}, {430, 434},
		{500, 501}, {510, 512}, {520, 522}, {530, 532},
		{508, 509}, {570, 572}, {580, 582}, {590, 592},
		{1000, 1000}, {1100, 1100}, {1110, 1112}, {1200, 1200}, {1210, 1212}, {1300, 1300}, {1310, 1312}, {1400, 1400}, {1410, 1412}, {1500, 1500}, {1510, 1512},
		{2000, 2003}, {2100, 2100}, {2110, 2112}, {2200, 2200}, {2210, 2218}, {2300, 2300}, {2310, 2312}, {2400, 2400}, {2410, 2412},
		{3000, 3001}, {3100, 3100}, {3110, 3112}, {3200, 3200}, {3210, 3212}, {3300, 3300}, {3310, 3312}, {3500, 3500}, {3510, 3512},
		{5000, 5000}, {5100, 5100}, {5110, 5112},
	} {
		for job := band[0]; job <= band[1]; job++ {
			validJobs[job] = true
		}
	}
}

func ValidJob(job int) bool { return validJobs[job] }

// SubJobFor is the Explorer subcategory a job implies: Dual Blade (43x)
// and Cannoneer (501/53x) branches need their creation sub job.
func SubJobFor(job int) int {
	switch {
	case job >= 430 && job <= 434:
		return SubJobDualBlade
	case IsCannoneerJob(job):
		return SubJobCannoneer
	}
	return SubJobNone
}

// DefaultJaguar is the Wild Hunter's first jaguar (Legacy quest 111112 value
// 10, riding mount 1932015) until jaguar capture quests exist.
const DefaultJaguar = 10
