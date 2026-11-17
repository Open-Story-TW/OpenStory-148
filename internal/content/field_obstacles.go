package content

import (
	"path"
	"strconv"
	"strings"
)

type fieldObstacle struct {
	area   MapArea
	damage int
}

// MatchesObstacleDamage uses the authored obstacle's collision area and damage
// around the current character position. The bounds cover each animation frame
// and allow for movement updates and the character's standing hitbox.
func (m Map) MatchesObstacleDamage(x, y, damage int) bool {
	for _, trap := range m.obstacles {
		a := trap.area
		if x >= a.Left-32 && x <= a.Right+32 && y >= a.Top-24 && y <= a.Bottom+48 && damage >= trap.damage*9/10 && damage <= trap.damage*11/10 {
			return true
		}
	}
	return false
}

func loadFieldObstacles(field, definitions *wzNode) []fieldObstacle {
	var obstacles []fieldObstacle
	for _, layer := range field.Children {
		objects := layer.child("obj")
		if objects == nil {
			continue
		}
		for _, placement := range objects.Children {
			if placement.stringValue("oS", "") != "trap" {
				continue
			}
			objectPath := path.Join(placement.stringValue("l0", ""), placement.stringValue("l1", ""), placement.stringValue("l2", ""))
			object := obstacleNode(definitions, objectPath)
			if object == nil || object.intValue("obstacle", 0) == 0 {
				continue
			}
			damage := object.intValue("damage", 0)
			if damage <= 0 || damage > 10000 {
				continue
			}
			x, y := placement.intValue("x", 0), placement.intValue("y", 0)
			flipped := placement.intValue("f", 0) != 0
			for _, frame := range object.Children {
				var canvas *wzNode
				switch frame.XMLName.Local {
				case "canvas":
					canvas = &frame
				case "uol":
					canvas = obstacleNode(definitions, path.Join(objectPath, frame.Value))
				}
				area, ok := obstacleFrameArea(canvas)
				if !ok {
					continue
				}
				if flipped {
					area.Left, area.Right = -area.Right, -area.Left
				}
				obstacles = append(obstacles, fieldObstacle{area: MapArea{
					Left: x + area.Left, Top: y + area.Top, Right: x + area.Right, Bottom: y + area.Bottom,
				}, damage: damage})
			}
		}
	}
	return obstacles
}

func obstacleNode(n *wzNode, property string) *wzNode {
	for _, name := range strings.Split(property, "/") {
		n = n.child(name)
	}
	return n
}

func obstacleFrameArea(canvas *wzNode) (MapArea, bool) {
	lt, rb := canvas.child("lt"), canvas.child("rb")
	if lt == nil || rb == nil {
		return MapArea{}, false
	}
	left, errLeft := strconv.Atoi(lt.X)
	top, errTop := strconv.Atoi(lt.Y)
	right, errRight := strconv.Atoi(rb.X)
	bottom, errBottom := strconv.Atoi(rb.Y)
	return MapArea{left, top, right, bottom}, errLeft == nil && errTop == nil && errRight == nil && errBottom == nil && left < right && top < bottom
}
