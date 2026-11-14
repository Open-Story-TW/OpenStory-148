package domain

import "testing"

func TestWireMaxMPSendsDemonFury(t *testing.T) {
	for _, tc := range []struct{ job, model, want int }{
		{3112, 1, 120}, {3112, 0, 120}, {3001, 1, 30}, {3110, 1, 50}, {3111, 1, 100}, {100, 1, 5}, {100, 0, 777},
	} {
		c := Character{Job: tc.job, VitalsModel: tc.model, BaseMaxMP: 5, MaxMP: 777}
		if got := c.WireMaxMP(); got != tc.want {
			t.Fatalf("job %d model %d: WireMaxMP=%d want %d", tc.job, tc.model, got, tc.want)
		}
	}
}
