package content

const (
	ZakumEye   = 4001017
	ZakumQuest = 100200
)

// ZakumAltar identifies an arena reactor and its monster family. Reactor
// coordinates and the width of the offering area are from v148 WZ.
type ZakumAltar struct {
	ReactorID, BaseMob int
	X, Y               int
}

func ZakumAltarByMap(mapID int) (ZakumAltar, bool) {
	switch mapID {
	case 280030000:
		return ZakumAltar{2111001, 8800000, -10, -204}, true
	case 280030001:
		return ZakumAltar{2111101, 8800100, -9, -203}, true
	default:
		return ZakumAltar{}, false
	}
}

// CanOffer uses the owner's ground drop origin, which is a few pixels
// lower than the reactor's area for a settled item.
func (a ZakumAltar) CanOffer(x, y int) bool {
	return x >= a.X-183 && x <= a.X+201 && y >= a.Y-30 && y <= a.Y
}
