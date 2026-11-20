package combat

import (
	"testing"
	"time"
)

func TestFatalVenomCapsStacksAndSeparatesOwners(t *testing.T) {
	now := time.Unix(1000, 0)
	apply := MobStatusApply{Stat: MobBurned, Poison: true, SourceSkill: 4340012, Value: 80,
		Prop: 50, Duration: 8 * time.Second, StackLimit: 3, DOTInterval: time.Second}
	effect := Effect{SkillID: 4341009}
	trace := MobBurns{}
	for at := 0; at < 5; at++ {
		st, ok := Resolve(apply, effect, 4916, 400, now.Add(time.Duration(at)*time.Millisecond),
			func(prop int) bool { return prop == 50 })
		st.OwnerSession = "actor-generation-one"
		st.OwnerGeneration = 1
		if !ok || !trace.Apply(st, now.Add(time.Duration(at)*time.Millisecond)) {
			t.Fatalf("application %d was lost", at)
		}
	}
	peer, ok := Resolve(apply, effect, 4920, 400, now, func(int) bool { return true })
	if !ok || !trace.Apply(peer, now) {
		t.Fatal("second owner's source was lost")
	}
	values := trace.Values()
	if len(values) != 2 || values[0].Damage != 80 || values[0].Stacks != 3 ||
		values[0].RemainingTicks != 8 || values[1].Stacks != 1 {
		t.Fatalf("unexpected active burns %+v", values)
	}
	hp, hits := trace.Tick(now.Add(time.Second), 1000)
	if hp != 680 || len(hits) != 2 {
		t.Fatalf("tick did not include both owners: hp=%d hits=%+v", hp, hits)
	}
	for _, hit := range hits {
		if hit.OwnerID == 4916 && (hit.Damage != 240 || hit.OwnerGeneration != 1 || hit.OwnerSession != "actor-generation-one") {
			t.Fatalf("stack damage or attribution changed: %+v", hit)
		}
	}
	hp, _ = trace.Tick(now.Add(9*time.Second), 25)
	if hp != 1 || len(trace.Expire(now.Add(9*time.Second))) != 2 || len(trace) != 0 {
		t.Fatalf("fatal venom killed an enemy or stayed after expiry: hp=%d burns=%+v", hp, trace)
	}
}
