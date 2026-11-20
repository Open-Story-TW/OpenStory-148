package content

import (
	"fmt"
	"path/filepath"
	"strconv"
	"time"
)

const (
	SnowballField = 109060000
	CoconutField  = 109080000
)

type SnowballLane struct {
	Y     int
	Entry string
}

type SnowballRules struct {
	Lanes                                      [2]SnowballLane
	ManX, BallX, Step, Finish, ManHP, Recovery int
	Sections                                   [3]int
	Damage                                     [3]int
	Freeze                                     time.Duration
}

func LoadSnowballRules(root string) (SnowballRules, error) {
	var rules SnowballRules
	field, err := readWZ(filepath.Join(root, "Map.wz", "Map", "Map1", "109060000.img.xml"))
	if err != nil {
		return rules, err
	}
	data := field.child("snowBall")
	if data == nil {
		return rules, fmt.Errorf("snowball rules absent from field %d", SnowballField)
	}
	for i := range rules.Lanes {
		lane := data.child(strconv.Itoa(i))
		if lane == nil || lane.stringValue("portal", "") == "" {
			return rules, fmt.Errorf("invalid snowball lane %d", i)
		}
		rules.Lanes[i] = SnowballLane{Y: lane.intValue("y", 0), Entry: lane.stringValue("portal", "")}
	}
	rules.ManX, rules.BallX = data.intValue("x", 0), data.intValue("x0", 0)
	rules.Step, rules.Finish = data.intValue("dx", 0), data.intValue("xMax", 0)
	rules.ManHP, rules.Recovery = data.intValue("snowManHP", 0), data.intValue("recoveryAmount", 0)
	rules.Sections = [3]int{data.intValue("section1", 0), data.intValue("section2", 0), data.intValue("section3", 0)}
	rules.Damage = [3]int{data.intValue("damageSnowBall", 0), data.intValue("damageSnowMan0", 0), data.intValue("damageSnowMan1", 0)}
	rules.Freeze = time.Duration(data.intValue("snowManWait", 0)) * time.Millisecond
	if rules.Step <= 0 || rules.Finish <= rules.Sections[2] || rules.Finish > 32767 || rules.ManHP <= 0 || rules.Recovery <= 0 ||
		rules.Sections[0] <= 0 || rules.Sections[1] <= rules.Sections[0] || rules.Sections[2] <= rules.Sections[1] ||
		rules.Damage[0] <= 0 || rules.Damage[1] <= 0 || rules.Damage[2] <= 0 || rules.Freeze <= 0 {
		return rules, fmt.Errorf("invalid snowball rules in field %d", SnowballField)
	}
	return rules, nil
}

type CoconutTarget struct{ X, Y int }

type CoconutRules struct {
	Targets                                    []CoconutTarget
	Fall, Bomb, Stop, Hits                     int
	Duration, Overtime, Finish                 time.Duration
	WinEffect, LoseEffect, WinSound, LoseSound string
}

func LoadCoconutRules(root string) (CoconutRules, error) {
	var rules CoconutRules
	field, err := readWZ(filepath.Join(root, "Map.wz", "Map", "Map1", "109080000.img.xml"))
	if err != nil {
		return rules, err
	}
	data := field.child("coconut")
	if data == nil {
		return rules, fmt.Errorf("coconut rules absent from field %d", CoconutField)
	}
	rules.Fall, rules.Bomb, rules.Stop = data.intValue("countFalling", 0), data.intValue("countBombing", 0), data.intValue("countStopped", 0)
	rules.Hits = data.intValue("countHit", 0)
	rules.Duration = time.Duration(data.intValue("timeDefault", 0)) * time.Second
	rules.Overtime = time.Duration(data.intValue("timeExpand", 0)) * time.Second
	rules.Finish = time.Duration(data.intValue("timeFinish", 0)) * time.Second
	rules.WinEffect, rules.LoseEffect = data.stringValue("effectWin", ""), data.stringValue("effectLose", "")
	rules.WinSound, rules.LoseSound = data.stringValue("soundWin", ""), data.stringValue("soundLose", "")
	for i := range 8 {
		layer := field.child(strconv.Itoa(i))
		if layer == nil {
			continue
		}
		objects := layer.child("obj")
		if objects == nil {
			continue
		}
		for j := range objects.Children {
			object := &objects.Children[j]
			if object.stringValue("oS", "") == "event" && object.stringValue("l0", "") == "sandBeach" && object.stringValue("l1", "") == "coconut" {
				rules.Targets = append(rules.Targets, CoconutTarget{X: object.intValue("x", 0), Y: object.intValue("y", 0)})
			}
		}
	}
	if len(rules.Targets) == 0 || len(rules.Targets) > 32767 || len(rules.Targets) != rules.Fall+rules.Bomb+rules.Stop ||
		rules.Fall < 1 || rules.Bomb < 0 || rules.Stop < 0 || rules.Hits <= 0 ||
		rules.Duration <= 0 || rules.Overtime <= 0 || rules.Finish <= 0 ||
		rules.WinEffect == "" || rules.LoseEffect == "" || rules.WinSound == "" || rules.LoseSound == "" {
		return rules, fmt.Errorf("invalid coconut rules in field %d", CoconutField)
	}
	return rules, nil
}
