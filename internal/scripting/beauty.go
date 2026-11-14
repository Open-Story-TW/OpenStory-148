package scripting

import (
	"errors"

	rt "github.com/arnodel/golua/runtime"
)

// styleList reads a Lua array of hair or face IDs, or skin colours (0-13).
func styleList(v rt.Value) ([]int64, error) {
	table, ok := v.TryTable()
	if !ok {
		return nil, errors.New("style list must be a table")
	}
	n := table.Len()
	if n < 1 || n > 255 {
		return nil, errors.New("style list requires 1..255 entries")
	}
	out := make([]int64, 0, n)
	for i := int64(1); i <= n; i++ {
		id, ok := table.Get(rt.IntValue(i)).TryInt()
		if !ok || id < 0 || id > 49999 || id > 13 && id < 20000 {
			return nil, errors.New("invalid hair, face or skin id")
		}
		out = append(out, id)
	}
	return out, nil
}

// askAvatar is Legacy cm.askAvatar: the client previews each listed hair or
// face on the character and the call returns the chosen ID. The host first
// drops IDs the game data does not have (the client cannot draw them) and
// duplicates; an empty remainder is an error.
func (s *Session) askAvatar(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
	if err := c.CheckNArgs(3); err != nil {
		return nil, err
	}
	text, err := c.StringArg(1)
	if err != nil {
		return nil, err
	}
	styles, err := styleList(c.Arg(2))
	if err != nil {
		return nil, err
	}
	if s.host != nil {
		styles, err = s.filterStyles(styles)
		if err != nil {
			return nil, err
		}
	}
	seen := map[int64]bool{}
	choices := make([]Choice, 0, len(styles))
	for _, id := range styles {
		if !seen[id] {
			seen[id] = true
			choices = append(choices, Choice{ID: id})
		}
	}
	if len(choices) == 0 {
		return nil, errors.New("no style in the list exists in game data")
	}
	return s.prompt(t, c, "avatar", text, choices)
}

func (s *Session) filterStyles(styles []int64) ([]int64, error) {
	result, err := s.runHost(Command{Kind: "style_filter", Ints: styles})
	if err != nil {
		return nil, err
	}
	kept, ok := result.Value.([]int64)
	if !ok {
		return nil, errors.New("style filter returned no list")
	}
	return kept, nil
}

// changeLook is Legacy cm.setAvatar / cm.setRandomAvatar: change_look(coupon,
// "hair"|"face", style) consumes one coupon and sets that style;
// change_look(coupon, kind, {styles}) draws one of them on the server. The
// result is {ok=true, style=id} or {ok=false, reason="no_coupon"}.
func (s *Session) changeLook(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
	if err := c.CheckNArgs(4); err != nil {
		return nil, err
	}
	coupon, err := c.IntArg(1)
	if err != nil {
		return nil, err
	}
	kind, err := c.StringArg(2)
	if err != nil {
		return nil, err
	}
	var code int64
	switch kind {
	case "hair":
		code = 1
	case "face":
		code = 2
	default:
		return nil, errors.New(`change_look kind must be "hair" or "face"`)
	}
	var styles []int64
	if id, ok := c.Arg(3).TryInt(); ok {
		styles = []int64{id}
	} else if styles, err = styleList(c.Arg(3)); err != nil {
		return nil, err
	}
	return s.execute(t, c, Command{Kind: "change_look", Ints: append([]int64{coupon, code}, styles...)})
}
