package content

import "strconv"

const (
	IceKnightName   = "Iceman"
	IceKnightLobby  = 932000000
	IceKnightFirst  = 932000100
	IceKnightSecond = 932000200
	IceKnightArena  = 932000300
	IceKnightResult = 932000400
	IceEscortMob    = 9300438
	IceKnightBoss   = 9300441
	IceKnightNPC    = 2159017
	IceKnightFrost  = 4001529
)

func iceKnightEvent() Event {
	return Event{
		Name: IceKnightName, ExitMap: IceKnightLobby, Seconds: 1200,
		PartyMin: 3, PartyMax: 6, Keep: 3, LevelMin: 50, LevelMax: 255,
		Maps: []int{IceKnightFirst, IceKnightSecond, IceKnightArena},
		Spawns: []EventSpawn{
			{MapID: IceKnightFirst, Life: Life{Kind: "m", TemplateID: IceEscortMob, X: -2046, Y: 93, Foothold: 71,
				RX0: -2070, RX1: 1867, RespawnSeconds: -1}},
			{MapID: IceKnightSecond, Life: Life{Kind: "m", TemplateID: IceEscortMob, X: -2792, Y: 93, Foothold: 99,
				RX0: -2880, RX1: 2175, RespawnSeconds: -1}},
			{MapID: IceKnightArena, Life: Life{Kind: "m", TemplateID: IceKnightBoss, X: -579, Y: 153, Foothold: 1,
				RX0: -620, RX1: -530, RespawnSeconds: -1}},
			{MapID: IceKnightArena, Life: Life{Kind: "n", TemplateID: IceKnightNPC, X: -579, Y: 153, Foothold: 1,
				RX0: -620, RX1: -530}},
		},
	}
}

type EscortNode struct {
	X, Y, Attribute int
	StopMillis      int
}

// escortRoute assembles the linear start-to-end path the controlled mob uses.
// WZ stores keyed nodes with branches; the wire carries ordered coordinates
// without node keys or edges.
func escortRoute(data *wzNode) []EscortNode {
	if data == nil {
		return nil
	}
	type waypoint struct {
		node  EscortNode
		edges []int
	}
	graph := make(map[int]waypoint)
	for _, entry := range data.Children {
		key := entry.intValue("key", -1)
		if key < 0 {
			continue
		}
		point := waypoint{node: EscortNode{X: entry.intValue("x", 0), Y: entry.intValue("y", 0), Attribute: entry.intValue("attr", 0)}}
		if stop := entry.child("stopInfo"); stop != nil {
			point.node.StopMillis = max(0, stop.intValue("stopDuration", 0)) * 1000
		}
		if edges := entry.child("edge"); edges != nil {
			for _, edge := range edges.Children {
				if key, err := strconv.Atoi(edge.Value); err == nil {
					point.edges = append(point.edges, key)
				}
			}
		}
		graph[key] = point
	}
	start, end := data.intValue("start", -1), data.intValue("end", -1)
	if _, ok := graph[start]; !ok {
		return nil
	}
	previous := map[int]int{start: start}
	queue := []int{start}
	for i := 0; i < len(queue); i++ {
		key := queue[i]
		if key == end {
			route := []EscortNode{graph[end].node}
			for key != start {
				key = previous[key]
				route = append(route, graph[key].node)
			}
			for l, r := 0, len(route)-1; l < r; l, r = l+1, r-1 {
				route[l], route[r] = route[r], route[l]
			}
			return route
		}
		for _, edge := range graph[key].edges {
			if _, visited := previous[edge]; visited {
				continue
			}
			if _, found := graph[edge]; found {
				previous[edge] = key
				queue = append(queue, edge)
			}
		}
	}
	return nil
}
