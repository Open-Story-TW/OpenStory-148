package protocol

import (
	"bytes"
	"testing"
)

func TestSceneNativePacketShapes(t *testing.T) {
	for _, c := range []struct{ got, want []byte }{{SceneKeyLock(true), []byte{0x65, 0x01, 1}}, {SceneKeyLock(false), []byte{0x65, 0x01, 0}}, {SceneUILock(true), []byte{0x66, 0x01, 1, 1, 0}}, {SceneUILock(false), []byte{0x66, 0x01, 0}}} {
		if !bytes.Equal(c.got, c.want) {
			t.Fatalf("got%x want%x", c.got, c.want)
		}
	}
	p := SceneEffect("Effect/Direction3.img/goLith/Scene0")
	r := NewReader(p[2:])
	if !bytes.Equal(p[:2], []byte{0x54, 0x01}) || r.U8() != 22 || r.String() != "Effect/Direction3.img/goLith/Scene0" || r.Err() != nil || r.Remaining() != 0 {
		t.Fatalf("malformed effect%x", p)
	}
	for _, path := range []string{"", "../foo", "Effect/Direction3.img/../bad", "Effect/Direction3.img/a\x00b"} {
		if SceneEffect(path) != nil {
			t.Fatalf("accepted %q", path)
		}
	}
}
