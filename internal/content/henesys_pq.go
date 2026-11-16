package content

const (
	HenesysPQName  = "HenesysPQ"
	HenesysPQMap   = 910010000
	HenesysPQLobby = 910010500
	HenesysPQExit  = 910010300
	MoonFlowerSeed = 4001453
	MoonRiceCake   = 4001101
	MoonBunny      = 9300061
)

// Moon flower sockets and the full moon are the seven reactor placements
// in TWMS v148 Map.wz/Map9/910010000.img. The six sockets all accept the
// same seed; Reactor.wz/910800[0–5].img uses item event type 9.
var MoonFlowerReactors = [...]Reactor{
	{TemplateID: 9108000, X: 4, Y: -690},
	{TemplateID: 9108001, X: 182, Y: -452},
	{TemplateID: 9108002, X: 0, Y: -210},
	{TemplateID: 9108003, X: -358, Y: -210},
	{TemplateID: 9108004, X: -535, Y: -449},
	{TemplateID: 9108005, X: -359, Y: -692},
}

var HenesysMoon = Reactor{TemplateID: 9101000, X: -183, Y: -433}

// MoonFlowerSeedFits applies the type 9 reactor's WZ lt/rb rectangle.
func MoonFlowerSeedFits(socket Reactor, x, y int) bool {
	return socket.X-19 <= x && x <= socket.X+20 && socket.Y-42 <= y && y <= socket.Y+16
}

func IsMoonBunnyAttacker(mobID int) bool { return mobID >= 9300062 && mobID <= 9300064 }
