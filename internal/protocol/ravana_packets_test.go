package protocol

import "testing"

func TestV148BurningSunburstUseRequest(t *testing.T) {
	body := []byte{0x65, 0x0f, 0x25, 0x02, 0x0a, 0x00, 0xd1, 0x16, 0x25, 0x00, 0x01, 0, 0, 0}
	request, err := ReadUseScriptedItem(NewReader(body))
	if err != nil || request.ClientTick != 0x2250f65 || request.Slot != 10 || request.TemplateID != 2430673 {
		t.Fatalf("native Sunburst request = %+v, err = %v", request, err)
	}
	body[10] = 2
	if _, err = ReadUseScriptedItem(NewReader(body)); err == nil {
		t.Fatal("scripted item batch count was accepted")
	}
}
