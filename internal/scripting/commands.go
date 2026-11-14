package scripting

import (
	"errors"
	"fmt"
	"strings"

	rt "github.com/arnodel/golua/runtime"
	"maple-go-server/internal/domain"
)

func (s *Session) warpCommand(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
	if err := c.CheckNArgs(3); err != nil {
		return nil, err
	}
	mapID, err := c.IntArg(1)
	if err != nil || mapID < 0 || mapID > 999999998 {
		return nil, errors.New("invalid warp map")
	}
	command := Command{Kind: "warp", Ints: []int64{mapID}}
	if name, ok := c.Arg(2).TryString(); ok {
		if name == "" || len(name) > 64 || strings.ContainsRune(name, '\x00') {
			return nil, errors.New("invalid warp portal name")
		}
		command.Text = name
	} else {
		portalID, err := c.IntArg(2)
		if err != nil || portalID < 0 || portalID > 255 {
			return nil, errors.New("invalid warp portal")
		}
		command.Ints = append(command.Ints, portalID)
	}
	return s.execute(t, c, command)
}

func (s *Session) questCommand(kind string) rt.GoFunctionFunc {
	return func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
		if err := c.CheckNArgs(2); err != nil {
			return nil, err
		}
		id, err := c.IntArg(1)
		if err != nil {
			return nil, err
		}
		if id < 1 || id > 65535 {
			return nil, errors.New("quest id out of range")
		}
		return s.execute(t, c, Command{Kind: kind, Ints: []int64{id}})
	}
}

func stringField(t *rt.Table, key string, required bool) (string, error) {
	v := t.Get(rt.StringValue(key))
	if v.IsNil() && !required {
		return "", nil
	}
	s, ok := v.TryString()
	if !ok || len(s) > 128 || (required && s == "") {
		return "", fmt.Errorf("invalid %s", key)
	}
	return s, nil
}
func intField(t *rt.Table, key string, required bool) (int64, error) {
	v := t.Get(rt.StringValue(key))
	if v.IsNil() && !required {
		return 0, nil
	}
	n, ok := v.TryInt()
	if !ok {
		return 0, fmt.Errorf("invalid %s", key)
	}
	return n, nil
}
func logKey(scope, period, name string) (domain.ScriptLogKey, error) {
	switch domain.LogScope(scope) {
	case domain.LogCharacter, domain.LogAccount, domain.LogWorld:
	default:
		return domain.ScriptLogKey{}, errors.New("invalid log scope")
	}
	switch domain.LogPeriod(period) {
	case domain.LogPermanent, domain.LogHourly, domain.LogDaily, domain.LogWeekly, domain.LogMonthly, domain.LogYearly:
	default:
		return domain.ScriptLogKey{}, errors.New("invalid log period")
	}
	if name == "" || len(name) > 128 {
		return domain.ScriptLogKey{}, errors.New("invalid log name")
	}
	return domain.ScriptLogKey{Scope: domain.LogScope(scope), Period: domain.LogPeriod(period), Name: name}, nil
}
func (s *Session) readLog(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
	if err := c.CheckNArgs(4); err != nil {
		return nil, err
	}
	scope, err := c.StringArg(1)
	if err != nil {
		return nil, err
	}
	period, err := c.StringArg(2)
	if err != nil {
		return nil, err
	}
	name, err := c.StringArg(3)
	if err != nil {
		return nil, err
	}
	key, err := logKey(scope, period, name)
	if err != nil {
		return nil, err
	}
	return s.execute(t, c, Command{Kind: "read_log", Log: &key})
}
func (s *Session) exchange(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
	if err := c.CheckNArgs(2); err != nil {
		return nil, err
	}
	spec, err := c.TableArg(1)
	if err != nil {
		return nil, err
	}
	request := domain.ScriptExchange{}
	request.MesosDelta, err = intField(spec, "mesos", false)
	if err != nil {
		return nil, err
	}
	if value := spec.Get(rt.StringValue("items")); !value.IsNil() {
		items, ok := value.TryTable()
		if !ok || items.Len() > 32 {
			return nil, errors.New("invalid exchange items")
		}
		for i := int64(1); i <= items.Len(); i++ {
			item, ok := items.Get(rt.IntValue(i)).TryTable()
			if !ok {
				return nil, errors.New("invalid item entry")
			}
			id, e := intField(item, "id", true)
			if e != nil {
				return nil, e
			}
			quantity, e := intField(item, "quantity", true)
			if e != nil {
				return nil, e
			}
			if id < 1 || id > 2147483647 || quantity == 0 || quantity < -30000 || quantity > 30000 {
				return nil, errors.New("item exchange values out of range")
			}
			request.Items = append(request.Items, domain.ScriptItemDelta{TemplateID: int(id), Quantity: int(quantity)})
		}
	}
	if value := spec.Get(rt.StringValue("logs")); !value.IsNil() {
		logs, ok := value.TryTable()
		if !ok || logs.Len() > 32 {
			return nil, errors.New("invalid exchange logs")
		}
		for i := int64(1); i <= logs.Len(); i++ {
			entry, ok := logs.Get(rt.IntValue(i)).TryTable()
			if !ok {
				return nil, errors.New("invalid log entry")
			}
			scope, e := stringField(entry, "scope", true)
			if e != nil {
				return nil, e
			}
			period, e := stringField(entry, "period", true)
			if e != nil {
				return nil, e
			}
			name, e := stringField(entry, "name", true)
			if e != nil {
				return nil, e
			}
			key, e := logKey(scope, period, name)
			if e != nil {
				return nil, e
			}
			delta, e := intField(entry, "delta", false)
			if e != nil {
				return nil, e
			}
			change := domain.ScriptLogChange{Key: key, Delta: delta}
			for _, field := range []string{"set", "expected", "maximum"} {
				if !entry.Get(rt.StringValue(field)).IsNil() {
					v, e := intField(entry, field, true)
					if e != nil {
						return nil, e
					}
					switch field {
					case "set":
						change.Set = &v
					case "expected":
						change.Expected = &v
					case "maximum":
						change.Maximum = &v
					}
				}
			}
			request.Logs = append(request.Logs, change)
		}
	}
	if value := spec.Get(rt.StringValue("quest_data")); !value.IsNil() {
		entries, ok := value.TryTable()
		if !ok || entries.Len() > 32 {
			return nil, errors.New("invalid quest data changes")
		}
		for i := int64(1); i <= entries.Len(); i++ {
			entry, ok := entries.Get(rt.IntValue(i)).TryTable()
			if !ok {
				return nil, errors.New("invalid quest data entry")
			}
			id, err := intField(entry, "id", true)
			if err != nil || id < 1 || id > 2147483647 {
				return nil, errors.New("quest data id out of range")
			}
			data, ok := entry.Get(rt.StringValue("data")).TryString()
			if !ok || len(data) > 4096 {
				return nil, errors.New("invalid quest data value")
			}
			change := domain.ScriptQuestChange{QuestID: int(id), Data: data}
			if expected := entry.Get(rt.StringValue("expected")); !expected.IsNil() {
				text, ok := expected.TryString()
				if !ok || len(text) > 4096 {
					return nil, errors.New("invalid quest data condition")
				}
				change.Expected = &text
			}
			request.QuestData = append(request.QuestData, change)
		}
	}
	if value := spec.Get(rt.StringValue("exp")); !value.IsNil() {
		exp, ok := value.TryInt()
		if !ok || exp < 0 || exp > 2147483647 {
			return nil, errors.New("invalid experience grant")
		}
		request.EXP = exp
	}
	if value := spec.Get(rt.StringValue("quests")); !value.IsNil() {
		entries, ok := value.TryTable()
		if !ok || entries.Len() > 32 {
			return nil, errors.New("invalid quest status changes")
		}
		for i := int64(1); i <= entries.Len(); i++ {
			entry, ok := entries.Get(rt.IntValue(i)).TryTable()
			if !ok {
				return nil, errors.New("invalid quest status entry")
			}
			id, err := intField(entry, "id", true)
			if err != nil || id < 1 || id > 65535 {
				return nil, errors.New("quest status id out of range")
			}
			state, err := intField(entry, "state", true)
			if err != nil || state != 1 && state != 2 {
				return nil, errors.New("quest status must be 1 or 2")
			}
			force := domain.ScriptQuestForce{QuestID: int(id), State: int(state)}
			if data := entry.Get(rt.StringValue("data")); !data.IsNil() {
				text, ok := data.TryString()
				if !ok || len(text) > 4096 {
					return nil, errors.New("invalid quest status data")
				}
				force.Data = &text
			}
			request.Quests = append(request.Quests, force)
		}
	}
	if len(request.Logs) == 0 && len(request.Items) == 0 && len(request.QuestData) == 0 && request.MesosDelta == 0 && request.EXP == 0 && len(request.Quests) == 0 {
		return nil, errors.New("empty exchange")
	}
	return s.execute(t, c, Command{Kind: "exchange", Exchange: &request})
}
