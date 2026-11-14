// qa-fixture-plan only reads the same job, registry and WZ authorities as the
// server. It never opens a database or prepares a live character.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	_ "maple-go-server/internal/skills/all"
	"os"
	"sort"
	"strconv"
	"strings"
)

type plannedSkill struct {
	ID    int `json:"skill_id"`
	Level int `json:"level"`
}
type fixturePlan struct {
	Job                         int            `json:"job"`
	Level                       int            `json:"level"`
	Skills                      []plannedSkill `json:"skills"`
	OrdinaryProgressionVerified bool           `json:"ordinary_progression_verified"`
}

func buildPlan(root string, job, level int, wanted []int) (fixturePlan, error) {
	plan := fixturePlan{Job: job, Level: level}
	if !domain.ValidJob(job) || level < 1 || level > 200 || len(wanted) < 1 || len(wanted) > 32 {
		return plan, fmt.Errorf("valid job, level1..200 and1..32 skills required")
	}
	state := map[int]byte{}
	var visit func(int) error
	visit = func(id int) error {
		if state[id] == 2 {
			return nil
		}
		if state[id] == 1 {
			return fmt.Errorf("cyclic skill prerequisite %d", id)
		}
		if id < 1 || id > 99999999 {
			return fmt.Errorf("invalid skill %d", id)
		}
		if _, ok := skills.Lookup(id); !ok {
			return fmt.Errorf("skill %d is not registered", id)
		}
		s, err := content.SkillDefinition(root, id)
		if err != nil {
			return err
		}
		if s.Disabled || !s.CanLearnBy(domain.Character{Job: job, Level: level, GM: domain.QAGMRank}) || level < s.ReqLevel || s.MaxLevel < 1 || s.MaxLevel > 255 {
			return fmt.Errorf("skill %d is unavailable to this QA job/level", id)
		}
		state[id] = 1
		ids := make([]int, 0, len(s.Requirements))
		for dep := range s.Requirements {
			ids = append(ids, dep)
		}
		sort.Ints(ids)
		for _, dep := range ids {
			if err = visit(dep); err != nil {
				return err
			}
			d, err := content.SkillDefinition(root, dep)
			if err != nil || d.MaxLevel < s.Requirements[dep] {
				return fmt.Errorf("invalid prerequisite %d for %d", dep, id)
			}
		}
		state[id] = 2
		plan.Skills = append(plan.Skills, plannedSkill{id, s.MaxLevel})
		return nil
	}
	for _, id := range wanted {
		if err := visit(id); err != nil {
			return plan, err
		}
	}
	return plan, nil
}
func main() {
	job := flag.Int("job", -1, "QA job ID")
	level := flag.Int("level", 200, "QA character level")
	list := flag.String("skills", "", "comma-separated requested skill IDs")
	root := flag.String("wz-root", os.Getenv("MAPLEGO_WZ_ROOT"), "audited WZ root; empty uses server seed")
	flag.Parse()
	var ids []int
	for _, s := range strings.Split(*list, ",") {
		id, err := strconv.Atoi(s)
		if err != nil {
			fmt.Fprintln(os.Stderr, "invalid requested skill ID")
			os.Exit(1)
		}
		ids = append(ids, id)
	}
	plan, err := buildPlan(*root, *job, *level, ids)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err = json.NewEncoder(os.Stdout).Encode(plan); err != nil {
		os.Exit(1)
	}
}
