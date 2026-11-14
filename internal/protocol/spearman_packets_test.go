package protocol

import (
	"maple-go-server/internal/domain"
	"testing"
)

func TestSpearmanLoginWireAcceptsOnlyReviewedJobAndMinimumLevel(t *testing.T) {
	c := domain.Character{ID: 1, Name: "Spearman", Job: 130, Level: 30, HP: 100, MaxHP: 100, MP: 20, MaxMP: 20}
	if !validCharacter(c) || len(SetField(c, 1)) == 0 {
		t.Fatal("reviewed130 login blocked")
	}
	c.Level = 29
	if validCharacter(c) {
		t.Fatal("premature130 accepted")
	}
	c.Level = 30
	// Any v145 job renders; GM/nonexistent jobs do not.
	for _, job := range []int{800, 900, 133, 600} {
		c.Job = job
		if validCharacter(c) {
			t.Fatal("nonexistent job enabled", job)
		}
	}
}
