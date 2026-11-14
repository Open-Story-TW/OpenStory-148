package skills

// OwnerOf returns the job package that owns a skill ID (skillID/10000 is the
// job), following the thread split of docs/SKILLS-PARALLEL-PLAN.md section 5.
// It returns "" for an ID no job thread owns.
func OwnerOf(skillID int) string {
	job := skillID / 10000
	switch {
	case job == 0 || job == 800 || job == 900 || job == 910 || job == 1000 || job >= 2000 && job <= 2005 || job == 3000 || job == 3001 || job >= 8000 && job <= 9204:
		return "common"
	case job >= 100 && job <= 132:
		return "warrior"
	case job >= 200 && job <= 232:
		return "magician"
	case job >= 300 && job <= 322:
		return "bowman"
	case job >= 400 && job <= 434:
		return "thief"
	case job >= 500 && job <= 532:
		return "pirate"
	case job >= 1100 && job <= 1512:
		return "cygnus"
	case job >= 2100 && job <= 2218:
		return "aranevan"
	case job >= 2300 && job <= 2412:
		return "mercphantom"
	case job >= 3100 && job <= 3212:
		return "resist1"
	case job >= 3300 && job <= 3512:
		return "resist2"
	}
	return ""
}
