// skilltest-plan turns rows of the skill checklist (docs/skill-audit/*/skill-validation.csv)
// into tools/skilltest cases, with expectations read from the same WZ and
// skill registry the server uses: max level, prerequisites, duration, target
// and line counts. It reads files only; it never opens a database or a client.
package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	_ "maple-go-server/internal/skills/all"
)

type testCase struct {
	ID              string         `json:"id"`
	Name            string         `json:"name,omitempty"`
	Kind            string         `json:"kind"`
	SkillID         int            `json:"skill_id"`
	SkillLevel      int            `json:"skill_level"`
	Job             int            `json:"job"`
	Level           int            `json:"level"`
	ExtraSkills     map[string]int `json:"extra_skills,omitempty"`
	ExpectDurationS int            `json:"expect_duration_s,omitempty"`
	Mobs            int            `json:"mobs,omitempty"`
	MinTargets      int            `json:"min_targets,omitempty"`
	MaxTargets      int            `json:"max_targets,omitempty"`
	Lines           int            `json:"lines,omitempty"`
	Mode            string         `json:"mode,omitempty"`
	Checks          []string       `json:"checks,omitempty"`
	Source          string         `json:"source"`
}

type skipped struct {
	SkillID int    `json:"skill_id"`
	Reason  string `json:"reason"`
}

type output struct {
	Cases   []testCase `json:"cases"`
	Skipped []skipped  `json:"skipped"`
}

// kindOf maps the checklist's 類型 column to one runner. Mixed types pick the
// part the runner can observe best; unsupported types are skipped, not guessed.
func kindOf(t string) string {
	switch {
	case strings.Contains(t, "召喚"):
		return "summon"
	case strings.Contains(t, "組隊Buff"):
		return "party_buff"
	case strings.HasPrefix(t, "攻擊"):
		return "attack"
	case strings.HasPrefix(t, "被動"):
		return "passive"
	case strings.HasPrefix(t, "Buff"):
		return "buff"
	}
	return ""
}

func prerequisites(root string, id int, out map[string]int, seen map[int]bool) error {
	if seen[id] {
		return nil
	}
	seen[id] = true
	s, err := content.SkillDefinition(root, id)
	if err != nil {
		return err
	}
	ids := make([]int, 0, len(s.Requirements))
	for dep := range s.Requirements {
		ids = append(ids, dep)
	}
	sort.Ints(ids)
	for _, dep := range ids {
		if _, ok := skills.Lookup(dep); !ok {
			return fmt.Errorf("prerequisite %d is not registered", dep)
		}
		d, err := content.SkillDefinition(root, dep)
		if err != nil {
			return err
		}
		out[strconv.Itoa(dep)] = d.MaxLevel // max level always satisfies the requirement
		if err := prerequisites(root, dep, out, seen); err != nil {
			return err
		}
	}
	return nil
}

func build(root string, rows [][]string, header map[string]int, level int, jobs, kinds, statuses map[string]bool) output {
	out := output{Cases: []testCase{}, Skipped: []skipped{}}
	col := func(r []string, name string) string {
		if i, ok := header[name]; ok && i < len(r) {
			return strings.TrimSpace(r[i])
		}
		return ""
	}
	for _, r := range rows {
		id, err := strconv.Atoi(col(r, "技能ID"))
		if err != nil {
			continue
		}
		job, _ := strconv.Atoi(col(r, "職業代碼"))
		if col(r, "WZ啟用") != "1" || col(r, "Go已註冊") != "1" {
			continue
		}
		if len(jobs) > 0 && !jobs[col(r, "職業代碼")] {
			continue
		}
		if len(statuses) > 0 && !statuses[col(r, "測試狀態")] {
			continue
		}
		kind := kindOf(col(r, "類型"))
		if kind == "" {
			out.Skipped = append(out.Skipped, skipped{id, "類型「" + col(r, "類型") + "」沒有對應的自動化案例"})
			continue
		}
		if len(kinds) > 0 && !kinds[kind] {
			continue
		}
		s, err := content.SkillDefinition(root, id)
		if err != nil || s.Disabled {
			out.Skipped = append(out.Skipped, skipped{id, "WZ 讀取失敗或停用"})
			continue
		}
		if !s.CanLearnBy(domain.Character{Job: job, Level: level, GM: domain.QAGMRank}) || level < s.ReqLevel {
			out.Skipped = append(out.Skipped, skipped{id, fmt.Sprintf("職業 %d／等級 %d 不能學", job, level)})
			continue
		}
		values, err := s.Values(s.MaxLevel)
		if err != nil {
			out.Skipped = append(out.Skipped, skipped{id, "最高等級數值無法計算"})
			continue
		}
		extra := map[string]int{}
		if err := prerequisites(root, id, extra, map[int]bool{}); err != nil {
			out.Skipped = append(out.Skipped, skipped{id, err.Error()})
			continue
		}
		c := testCase{ID: fmt.Sprintf("%s-%d", kind, id), Name: col(r, "技能名稱"), Kind: kind, SkillID: id,
			SkillLevel: s.MaxLevel, Job: job, Level: level, Source: "WZ+registry via cmd/skilltest-plan"}
		if len(extra) > 0 {
			c.ExtraSkills = extra
		}
		switch kind {
		case "buff", "party_buff":
			c.ExpectDurationS = values["time"]
			c.Checks = []string{"map_change", "channel_change", "expiry"}
		case "summon":
			c.ExpectDurationS = values["time"]
		case "attack":
			mobs := values["mobCount"]
			if mobs < 1 {
				mobs = 1
			}
			c.MaxTargets = mobs
			c.Mobs = min(mobs+1, domain.QAMobMaxBatch) // one extra mob shows the target cap
			c.MinTargets = min(mobs, 3)                // QA mobs stand 32px apart; wide caps may exceed range
			c.Lines = values["attackCount"]
			if s.Info["rapidAttack"] == 1 || s.Info["keydown"] == 1 {
				c.Mode = "keydown"
			}
		}
		out.Cases = append(out.Cases, c)
	}
	return out
}

func set(v string) map[string]bool {
	out := map[string]bool{}
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out[p] = true
		}
	}
	return out
}

func main() {
	path := flag.String("csv", "", "skill-validation.csv from docs/skill-audit/<date>")
	root := flag.String("wz-root", os.Getenv("MAPLEGO_WZ_ROOT"), "audited WZ root; empty uses the server seed")
	level := flag.Int("level", 200, "QA character level used for every case")
	jobs := flag.String("jobs", "", "comma-separated 職業代碼 filter, e.g. 210,211,212")
	kinds := flag.String("kinds", "", "comma-separated kinds: passive,buff,party_buff,attack,summon")
	statuses := flag.String("status", "", "comma-separated 測試狀態 filter, e.g. 無可核對實機紀錄")
	flag.Parse()
	if *path == "" || *level < 1 || *level > 200 {
		fmt.Fprintln(os.Stderr, "usage: skilltest-plan -csv <skill-validation.csv> [-jobs 212] [-kinds attack]")
		os.Exit(2)
	}
	f, err := os.Open(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	rows, header, err := readCSV(f)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(build(*root, rows, header, *level, set(*jobs), set(*kinds), set(*statuses))); err != nil {
		os.Exit(1)
	}
}

func readCSV(r io.Reader) ([][]string, map[string]int, error) {
	all, err := csv.NewReader(r).ReadAll()
	if err != nil || len(all) == 0 {
		return nil, nil, fmt.Errorf("unreadable checklist CSV")
	}
	header := map[string]int{}
	for i, h := range all[0] {
		header[strings.TrimPrefix(strings.TrimSpace(h), "\ufeff")] = i
	}
	return all[1:], header, nil
}
