package scripting

import (
	"errors"
	"fmt"
	"strings"
	"time"

	rt "github.com/arnodel/golua/runtime"
)

// installScene adds what a cutscene needs on top of a conversation: timed
// waits the client answers itself, lines spoken by the player or by another
// NPC, picture pages, and host operations (lock the controls, walk the
// character, play an effect). The host decides which scripts get this API.
func (s *Session) installScene(bind func(string, rt.GoFunctionFunc, int)) {
	bind("gender", s.simpleCommand("gender", 0), 1)
	// wait(ms): the script continues when the client reports the time is up.
	bind("wait", func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
		if err := c.CheckNArgs(2); err != nil {
			return nil, err
		}
		ms, err := c.IntArg(1)
		if err != nil || ms < 1 || ms > 60000 {
			return nil, errors.New("wait must be 1-60000 ms")
		}
		return s.scenePrompt(t, c, Prompt{Kind: "wait", Wait: int(ms)})
	}, 2)
	// say_as(text, speaker[, back[, next]]): speaker 0 is the player, any
	// other number the template id of the NPC shown as talking.
	bind("say_as", func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
		if err := c.CheckNArgs(3); err != nil {
			return nil, err
		}
		text, err := c.StringArg(1)
		if err != nil {
			return nil, err
		}
		speaker, err := c.IntArg(2)
		if err != nil || speaker < 0 || speaker > 9999999 {
			return nil, errors.New("invalid speaker")
		}
		back, next := false, true
		if c.NArgs() > 3 && !c.Arg(3).IsNil() {
			if back, err = c.BoolArg(3); err != nil {
				return nil, err
			}
		}
		if c.NArgs() > 4 && !c.Arg(4).IsNil() {
			if next, err = c.BoolArg(4); err != nil {
				return nil, err
			}
		}
		p := Prompt{Kind: "say", Text: text, AllowBack: back, AllowNext: next, Speaker: 1, SpeakerNPC: int(speaker)}
		if speaker == 0 {
			p.Speaker = 3
		}
		return s.scenePrompt(t, c, p)
	}, 5)
	// show_images{path, ...}: one window of picture pages.
	bind("show_images", func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
		if err := c.CheckNArgs(2); err != nil {
			return nil, err
		}
		list, err := c.TableArg(1)
		if err != nil || list.Len() < 1 || list.Len() > 16 {
			return nil, errors.New("show_images takes 1-16 paths")
		}
		p := Prompt{Kind: "image"}
		for i := int64(1); i <= list.Len(); i++ {
			path, ok := list.Get(rt.IntValue(i)).TryString()
			if !ok || path == "" || len(path) > 160 || strings.ContainsAny(path, "\\\x00") || strings.Contains(path, "..") {
				return nil, errors.New("invalid image path")
			}
			p.Choices = append(p.Choices, Choice{ID: i, Text: path})
		}
		return s.scenePrompt(t, c, p)
	}, 2)
	// direct(op, ...): numbers and at most one string, checked by the host.
	bind("direct", func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
		if err := c.CheckNArgs(2); err != nil {
			return nil, err
		}
		op, err := c.StringArg(1)
		if err != nil || op == "" || len(op) > 32 {
			return nil, errors.New("invalid scene operation")
		}
		command := Command{Kind: "scene_" + op}
		for i := 2; i < c.NArgs(); i++ {
			if n, ok := c.Arg(i).TryInt(); ok {
				if len(command.Ints) >= 8 {
					return nil, errors.New("too many scene arguments")
				}
				command.Ints = append(command.Ints, n)
				continue
			}
			text, ok := c.Arg(i).TryString()
			if !ok || command.Text != "" || text == "" || len(text) > 160 || strings.ContainsRune(text, 0) {
				return nil, fmt.Errorf("invalid scene argument %d", i)
			}
			command.Text = text
		}
		return s.execute(t, c, command)
	}, 10)
}

func (s *Session) scenePrompt(t *rt.Thread, c *rt.GoCont, p Prompt) (rt.Cont, error) {
	if len(p.Text) > 8192 || s.prompts >= s.limits.MaxPrompts {
		return nil, errors.New("prompt limit exceeded")
	}
	s.prompts++
	p.ID = fmt.Sprintf("%s-%d", s.nonce, s.prompts)
	p.Deadline = time.Now().Add(s.limits.PromptTimeout)
	s.event = Event{Kind: "prompt", Script: s.script.Name, SHA256: s.script.SHA256, Prompt: &p}
	values, err := t.Yield(nil)
	if err != nil {
		return nil, err
	}
	if s.cancelRequested.Load() {
		return nil, ErrCancelled
	}
	return c.PushingNext(t.Runtime, values...), nil
}
