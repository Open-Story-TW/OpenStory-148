package domain

import "os"

// QAWarpMaps is the reviewed QA !warp allowlist (Raymond approved QA warps
// for skill acceptance): job-advancement towns and one training field per
// level band, each with a WZ spawn portal. Levels are the fields' mob levels
// in the Legacy WZ.
var QAWarpMaps = map[int]string{
	QAMobMap:     "empty QA monster field",
	QAPharaohMap: "Nett's Pyramid normal stage 1 (Pharaoh's Wrath QA)",
	915000300:    "Phantom native guard-lure skill tutorial",
	100000000: "Henesys", 101000000: "Ellinia", 102000000: "Perion", 103000000: "Kerning City",
	104000000: "Lith Harbor", 211000000: "El Nath", 220000000: "Ludibrium", 240000000: "Leafre",
	104010000: "mobs 1-2", 101010100: "mobs 5-10", 102020400: "mobs 12-14", 102030000: "mobs 19-20",
	101040000: "mobs 26", 200010100: "mobs 36", 200040000: "mobs 41", 105010000: "mobs 50",
	222010000: "mobs 55", 220020600: "mobs 61-63", 230020000: "mobs 64", 260020000: "mobs 76",
	250010300: "mobs 81", 541010010: "mobs 93", 220060000: "mobs 95-97", 240010200: "mobs 101-105",
	240020100: "mobs 118", 270010100: "mobs 136-142", 240040600: "mobs 153",
}

// QAWarpDojoEnv adds the Mu Lung Dojo hall (925020001, So Gong) to the QA
// !warp targets when set to 1. It is off by default: the reviewed list above
// stays as approved, and an isolated QA world turns the hall on to test the
// dojo without walking from Mu Lung.
const QAWarpDojoEnv = "MAPLEGO_QA_WARP_DOJO"

const qaWarpDojoHall = 925020001

// QAWarpAllowed reports whether QA !warp may target mapID.
func QAWarpAllowed(mapID int) bool {
	if QAWarpMaps[mapID] != "" {
		return true
	}
	return mapID == qaWarpDojoHall && os.Getenv(QAWarpDojoEnv) == "1"
}

// QADojoEnergyEnv enables the QA !dojoenergy command, which fills the caller's
// Mu Lung Dojo energy gauge, when set to 1. It is off by default and exists
// so an isolated QA world can test the three energy skills without charging
// the gauge by hand; the energy rules themselves are unchanged.
const QADojoEnergyEnv = "MAPLEGO_QA_DOJO_ENERGY"

// QADojoEnergyEnabled reports whether !dojoenergy is on for this process.
func QADojoEnergyEnabled() bool { return os.Getenv(QADojoEnergyEnv) == "1" }
