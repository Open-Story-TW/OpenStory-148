// Quest layouts follow the retained native CQuestMan path at 0x63A4D8
// (accept/complete/scripted), 0x932CE3 (forfeit), plus Legacy packet sources.
package protocol

import (
	"fmt"
	"maple-go-server/internal/domain"
	"sort"
	"time"
)

const RecvQuestAction uint16 = 0xb5
const SendQuestInfo uint16 = 0x159

type QuestActionRequest struct {
	Action                    uint8
	QuestID, NPCID, Selection int
	ItemSlot                  int
	X, Y                      int16
}

func ReadQuestAction(r *Reader) (QuestActionRequest, error) {
	q := QuestActionRequest{Action: r.U8(), QuestID: int(r.U16())}
	switch q.Action {
	case 1:
		q.NPCID = int(r.I32())
		// Native CQuestMan accepts include the quest cash-item slot, followed
		// by coordinates except for the cash-quest branch. Position is not
		// authority: the world rechecks the character's actual NPC proximity.
		if r.Remaining() != 4 && r.Remaining() != 8 {
			return q, ErrMalformed
		}
		q.ItemSlot = int(r.I32())
		if r.Remaining() == 4 {
			q.X, q.Y = r.I16(), r.I16()
		}
	case 2:
		q.NPCID = int(r.I32())
		if r.Remaining() != 8 && r.Remaining() != 12 {
			return q, ErrMalformed
		}
		q.ItemSlot = int(r.I32())
		if r.Remaining() == 8 {
			q.X, q.Y = r.I16(), r.I16()
		}
		q.Selection = int(r.I32())
	case 4, 5:
		q.NPCID = int(r.I32())
		q.X, q.Y = r.I16(), r.I16()
	case 3:
	default:
		return q, ErrMalformed
	}
	if r.Err() != nil || r.Remaining() != 0 || q.QuestID < 1 || (q.Action != 3 && q.NPCID < 1) {
		return q, ErrMalformed
	}
	return q, nil
}

func QuestProgress(q domain.QuestState) string {
	ids := q.MobOrder
	if len(ids) == 0 {
		ids = make([]int, 0, len(q.MobKills))
		for id := range q.MobKills {
			ids = append(ids, id)
		}
		sort.Ints(ids)
	}
	value := ""
	for _, id := range ids {
		value += fmt.Sprintf("%03d", max(0, min(999, q.MobKills[id])))
	}
	return value
}
func QuestUpdate(q domain.QuestState) []byte {
	w := NewWriter(0x36)
	w.U8(1)
	w.U16(uint16(q.ID))
	w.U8(uint8(q.State))
	switch q.State {
	case 0:
		w.U8(0)
	case 1:
		w.String(QuestProgress(q))
	case 2:
		w.I64(mapleTime(q.CompletedAt))
	default:
		return nil
	}
	return w.Packet()
}

// QuestRecord is a started quest whose progress string is script data.
func QuestRecord(questID int, data string) []byte {
	if questID < 1 || questID > 65535 {
		return nil
	}
	w := NewWriter(0x36)
	w.U8(1)
	w.U16(uint16(questID))
	w.U8(1)
	w.String(data)
	return w.Packet()
}
func QuestMobUpdate(q domain.QuestState) []byte {
	w := NewWriter(0x36)
	w.U8(1)
	w.U16(uint16(q.ID))
	w.U8(1)
	w.String(QuestProgress(q))
	w.I64(0)
	return w.Packet()
}
func QuestInfo(questID, npcID, progress, nextQuest int) []byte {
	if questID < 1 || questID > 65535 || nextQuest < 0 || nextQuest > 65535 {
		return nil
	}
	w := NewWriter(SendQuestInfo)
	w.U8(uint8(progress))
	w.U16(uint16(questID))
	w.I32(int32(npcID))
	// CUserLocal::OnQuestResult native 0x87BCF9 mode10 consumes a WORD.
	w.U16(uint16(nextQuest))
	return w.Packet()
}

// Completed quests in the initial character snapshot use Legacy's compressed
// KoreanDateUtil minute timestamp, whereas live QuestUpdate uses FILETIME.
func questSnapshotTime(t time.Time) int64 { return int64(float64(t.Unix()/60)*0.1396987) + 27111908 }

// A started quest without monster counters shows its custom data record, as
// Legacy does for a quest force-started with data.
func AddQuestSections(w *Writer, quests []domain.QuestState, records map[int]string) {
	ordered := append([]domain.QuestState(nil), quests...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	for _, state := range []int{1, 2} {
		count := 0
		for _, q := range ordered {
			if q.State == state {
				count++
			}
		}
		w.U8(1)
		w.U16(uint16(count))
		for _, q := range ordered {
			if q.State != state {
				continue
			}
			w.U16(uint16(q.ID))
			if state == 1 {
				progress := QuestProgress(q)
				if progress == "" {
					progress = records[q.ID]
				}
				w.String(progress)
			} else {
				w.I64(questSnapshotTime(q.CompletedAt))
			}
		}
	}
}
