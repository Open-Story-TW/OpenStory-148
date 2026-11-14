package content

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type SceneTransition struct {
	TargetMap int
	After     time.Duration
}
type MapScene struct {
	Path       string
	Transition SceneTransition
}

var scenePathPattern = regexp.MustCompile(`^Effect/(Direction[0-9]+\.img)/([A-Za-z0-9_]+(?:/[A-Za-z0-9_]+){1,4})$`)

// A scene grants only the field transition encoded in its mounted WZ. The
// first supported lifecycle has one terminal transfer, not arbitrary Lua warp.
func LoadMapScene(root, path string) (MapScene, error) {
	matches := scenePathPattern.FindStringSubmatch(path)
	if root == "" || matches == nil {
		return MapScene{}, fmt.Errorf("invalid mounted scene path")
	}
	n, err := readWZ(filepath.Join(root, "Effect.wz", matches[1]+".xml"))
	if err != nil {
		return MapScene{}, err
	}
	for _, name := range strings.Split(matches[2], "/") {
		n = n.child(name)
		if n == nil {
			return MapScene{}, fmt.Errorf("scene not found: %s", path)
		}
	}
	if len(n.Children) == 0 || len(n.Children) > 512 {
		return MapScene{}, fmt.Errorf("scene action count is out of range")
	}
	result := MapScene{Path: path}
	transfers := 0
	for _, step := range n.Children {
		if step.intValue("type", -1) != 2 {
			continue
		}
		transfers++
		target, start := step.intValue("field", -1), step.intValue("start", -1)
		if target < 0 || target > 999999998 || start < 0 || start > 30000 {
			return MapScene{}, fmt.Errorf("scene transition is out of range")
		}
		result.Transition = SceneTransition{TargetMap: target, After: time.Duration(start) * time.Millisecond}
	}
	if transfers != 1 {
		return MapScene{}, fmt.Errorf("scene must have one terminal field transfer")
	}
	return result, nil
}
