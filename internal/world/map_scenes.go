package world

import "fmt"

// entryScenes lists the reviewed onUserEnter scripts whose whole Legacy
// behaviour (MapScriptMethods.showIntro) is to lock the UI and play one
// mounted WZ scene that ends in a field transfer. Each entry is bound to the
// maps Legacy played it on; the scene path depends only on the map and the
// character's gender, so the host can check exactly what a script asks for.
var entryScenes = map[string]map[int]string{
	// %d is the gender (0 or 1).
	"goLith":         {2010000: "Effect/Direction3.img/goLith/Scene%d"},
	"goSwordman":     {1020100: "Effect/Direction3.img/swordman/Scene%d"},
	"goMagician":     {1020200: "Effect/Direction3.img/magician/Scene%d"},
	"goRogue":        {1020400: "Effect/Direction3.img/rogue/Scene%d"},
	"goPirate":       {1020500: "Effect/Direction3.img/pirate/Scene%d"},
	"meetWithDragon": {900090100: "Effect/Direction4.img/meetWithDragon/Scene%d"},
	"crash_Dragon":   {900090102: "Effect/Direction4.img/crash/Scene%d"},
	"getDragonEgg":   {900090103: "Effect/Direction4.img/getDragonEgg/Scene%d"},
	"Resi_tutor70":   {931000021: "Effect/Direction4.img/Resistance/TalkJ"},
	"cygnusTest": {
		913040000: "Effect/Direction.img/cygnus/Scene0", 913040001: "Effect/Direction.img/cygnus/Scene1",
		913040002: "Effect/Direction.img/cygnus/Scene2", 913040003: "Effect/Direction.img/cygnus/Scene3",
		913040004: "Effect/Direction.img/cygnus/Scene4", 913040005: "Effect/Direction.img/cygnus/Scene5",
		913040006: "Effect/Direction.img/cygnus/Scene9",
	},
	"cygnusJobTutorial": {
		913040100: "Effect/Direction.img/cygnusJobTutorial/Scene0", 913040101: "Effect/Direction.img/cygnusJobTutorial/Scene1",
		913040102: "Effect/Direction.img/cygnusJobTutorial/Scene2", 913040103: "Effect/Direction.img/cygnusJobTutorial/Scene3",
		913040104: "Effect/Direction.img/cygnusJobTutorial/Scene4", 913040105: "Effect/Direction.img/cygnusJobTutorial/Scene5",
		913040106: "Effect/Direction.img/cygnusJobTutorial/Scene6",
	},
	"aranDirection": {
		914090010: "Effect/Direction1.img/aranTutorial/Scene0", 914090011: "Effect/Direction1.img/aranTutorial/Scene1%d",
		914090012: "Effect/Direction1.img/aranTutorial/Scene2%d", 914090013: "Effect/Direction1.img/aranTutorial/Scene3",
		914090100: "Effect/Direction1.img/aranTutorial/HandedPoleArm%d", 914090200: "Effect/Direction1.img/aranTutorial/Maha",
	},
	"evanPromotion": {
		900090000: "Effect/Direction4.img/promotion/Scene0%d", 900090001: "Effect/Direction4.img/promotion/Scene1",
		900090002: "Effect/Direction4.img/promotion/Scene2%d", 900090003: "Effect/Direction4.img/promotion/Scene3",
	},
}

// starterEntryScripts names the onUserEnter script of the job intro maps that
// are part of the embedded starter content, which records no script names.
var starterEntryScripts = map[int]string{1020100: "goSwordman", 1020200: "goMagician", 1020400: "goRogue", 1020500: "goPirate"}

// entryScenePath is the scene the onUserEnter script plays for a character of
// this gender on this map; ok is false when the script has no reviewed scene
// there.
func entryScenePath(script string, mapID, gender int) (path string, ok bool) {
	format, ok := entryScenes[script][mapID]
	if !ok {
		return "", false
	}
	if gender != 0 {
		gender = 1
	}
	for i := 0; i+1 < len(format); i++ {
		if format[i] == '%' {
			return fmt.Sprintf(format, gender), true
		}
	}
	return format, true
}

// entrySceneScript maps a map-entry source name back to its script name.
func entrySceneScript(source string) string {
	const prefix, suffix = "map/on_user_enter/", ".lua"
	if len(source) <= len(prefix)+len(suffix) || source[:len(prefix)] != prefix || source[len(source)-len(suffix):] != suffix {
		return ""
	}
	return source[len(prefix) : len(source)-len(suffix)]
}
