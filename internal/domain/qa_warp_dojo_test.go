package domain

import "testing"

func TestQAWarpDojoHallIsOffByDefault(t *testing.T) {
	t.Setenv(QAWarpDojoEnv, "")
	if QAWarpAllowed(925020001) || !QAWarpAllowed(100000000) || QAWarpAllowed(925020100) {
		t.Fatal("default QA warp targets changed")
	}
	t.Setenv(QAWarpDojoEnv, "1")
	if !QAWarpAllowed(925020001) || QAWarpAllowed(925020000) || QAWarpAllowed(925020100) || QAWarpMaps[925020001] != "" {
		t.Fatal("the switch must add the dojo hall only")
	}
}
