package scripting

import (
	"fmt"
	"sort"

	"github.com/arnodel/golua/ast"
	"github.com/arnodel/golua/astcomp"
	"github.com/arnodel/golua/code"
	"github.com/arnodel/golua/ir"
	"github.com/arnodel/golua/ircomp"
	rt "github.com/arnodel/golua/runtime"
)

// Compile through the VM's public AST/IR API. This keeps original source
// locations while inserting statement checkpoints whose arguments capture
// lexical locals. No unsafe/reflection access to the VM register array is used.
func compile(r *rt.Runtime, name string, source []byte) (*rt.Closure, error) {
	block, size, err := r.ParseLuaChunk(name, source)
	if err != nil {
		return nil, err
	}
	defer r.ReleaseMem(size)
	r.RequireMem(uint64(len(source)) * 32)
	r.RequireCPU(uint64(len(source))*16 + 1)
	transformed, err := instrumentBlock(*block, nil, 0)
	if err != nil {
		return nil, err
	}
	// Capture the private hook as a lexical upvalue before user code can mutate
	// the environment. Register rejects this reserved source identifier.
	bind := ast.NewLocalStat(nil, []ast.NameAttrib{ast.NewNameAttrib(ast.Name{Val: "__maple_trace"}, nil)}, []ast.ExpNode{ast.Name{Val: "__maple_trace"}})
	transformed.Stats = append([]ast.Stat{bind}, transformed.Stats...)
	k, constants, err := astcomp.CompileLuaChunk(name, transformed)
	if err != nil {
		return nil, err
	}
	constants = ir.FoldConstants(constants, ir.DefaultFold)
	c := ircomp.NewConstantCompiler(constants, code.NewBuilder(name))
	c.QueueConstant(k)
	unit, err := c.CompileQueue()
	if err != nil {
		return nil, err
	}
	return r.LoadLuaUnit(unit, rt.TableValue(r.GlobalEnv())), nil
}

func traceStatement(line int, names []string) ast.Stat {
	uniq := map[string]bool{}
	for _, name := range names {
		if name != "_ENV" && name != "__maple_trace" {
			uniq[name] = true
		}
	}
	ordered := make([]string, 0, len(uniq))
	for n := range uniq {
		ordered = append(ordered, n)
	}
	sort.Strings(ordered)
	fields := make([]ast.TableField, 0, len(ordered))
	for _, n := range ordered {
		// The wrapper table preserves names whose current value is nil.
		wrapper := ast.TableConstructor{Fields: []ast.TableField{ast.NewTableField(ast.String{Val: []byte("value")}, ast.Name{Val: n})}}
		fields = append(fields, ast.NewTableField(ast.String{Val: []byte(n)}, wrapper))
	}
	return ast.NewFunctionCall(ast.Name{Val: "__maple_trace"}, ast.Name{}, []ast.ExpNode{ast.NewInt(uint64(max(1, line))), ast.TableConstructor{Fields: fields}})
}

func sourceLine(n ast.Locator, fallback int) int {
	p := n.Locate().StartPos()
	if p == nil {
		return max(1, fallback)
	}
	return p.Line
}

func instrumentBlock(block ast.BlockStat, outer []string, fallback int) (ast.BlockStat, error) {
	names := append([]string(nil), outer...)
	stats := make([]ast.Stat, 0, len(block.Stats)*2+1)
	if len(names) > 128 {
		return block, fmt.Errorf("too many lexical locals for debugger")
	}
	for _, stat := range block.Stats {
		line := sourceLine(stat, fallback)
		stats = append(stats, traceStatement(line, names))
		next, err := instrumentStat(stat, names, line)
		if err != nil {
			return block, err
		}
		stats = append(stats, next)
		switch n := stat.(type) {
		case ast.LocalStat:
			for _, a := range n.NameAttribs {
				names = append(names, a.Name.Val)
			}
		case ast.LocalFunctionStat:
			names = append(names, n.Name.Val)
		}
	}
	if block.Return != nil && len(block.Return) > 0 {
		stats = append(stats, traceStatement(sourceLine(block.Return[0], fallback), names))
	}
	if len(stats) == 0 && fallback > 0 {
		stats = append(stats, traceStatement(fallback, names))
	}
	block.Stats = stats
	for i, e := range block.Return {
		x, err := instrumentExp(e, names, fallback)
		if err != nil {
			return block, err
		}
		block.Return[i] = x
	}
	return block, nil
}

func instrumentFunction(f ast.Function, names []string, line int) (ast.Function, error) {
	locals := append([]string(nil), names...)
	for _, p := range f.Params {
		locals = append(locals, p.Val)
	}
	if f.VarargName != nil {
		locals = append(locals, f.VarargName.Val)
	}
	b, err := instrumentBlock(f.Body, locals, line)
	f.Body = b
	return f, err
}

func instrumentStat(stat ast.Stat, names []string, line int) (ast.Stat, error) {
	var err error
	switch n := stat.(type) {
	case ast.LocalStat:
		for i, e := range n.Values {
			n.Values[i], err = instrumentExp(e, names, line)
			if err != nil {
				return nil, err
			}
		}
		return n, nil
	case ast.GlobalStat:
		for i, e := range n.Values {
			n.Values[i], err = instrumentExp(e, names, line)
			if err != nil {
				return nil, err
			}
		}
		return n, nil
	case ast.AssignStat:
		for i, e := range n.Src {
			n.Src[i], err = instrumentExp(e, names, line)
			if err != nil {
				return nil, err
			}
		}
		return n, nil
	case ast.LocalFunctionStat:
		n.Function, err = instrumentFunction(n.Function, append(append([]string(nil), names...), n.Name.Val), line)
		return n, err
	case ast.GlobalFunctionStat:
		n.Function, err = instrumentFunction(n.Function, names, line)
		return n, err
	case ast.FunctionCall:
		e, err := instrumentExp(n, names, line)
		if err != nil {
			return nil, err
		}
		return e.(ast.FunctionCall), nil
	case ast.BlockStat:
		return instrumentBlock(n, names, line)
	case ast.IfStat:
		n.If.Cond, err = instrumentExp(n.If.Cond, names, line)
		if err != nil {
			return nil, err
		}
		n.If.Body, err = instrumentBlock(n.If.Body, names, line)
		if err != nil {
			return nil, err
		}
		for i, c := range n.ElseIfs {
			c.Cond, err = instrumentExp(c.Cond, names, line)
			if err != nil {
				return nil, err
			}
			c.Body, err = instrumentBlock(c.Body, names, line)
			if err != nil {
				return nil, err
			}
			n.ElseIfs[i] = c
		}
		if n.Else != nil {
			b, e := instrumentBlock(*n.Else, names, line)
			if e != nil {
				return nil, e
			}
			n.Else = &b
		}
		return n, nil
	case ast.WhileStat:
		n.Cond, err = instrumentExp(n.Cond, names, line)
		if err != nil {
			return nil, err
		}
		n.Body, err = instrumentBlock(n.Body, names, line)
		return n, err
	case ast.RepeatStat:
		n.Body, err = instrumentBlock(n.Body, names, line)
		if err != nil {
			return nil, err
		}
		n.Cond, err = instrumentExp(n.Cond, names, line)
		return n, err
	case *ast.ForStat:
		v := *n
		v.Start, err = instrumentExp(v.Start, names, line)
		if err != nil {
			return nil, err
		}
		v.Stop, err = instrumentExp(v.Stop, names, line)
		if err != nil {
			return nil, err
		}
		if v.Step != nil {
			v.Step, err = instrumentExp(v.Step, names, line)
			if err != nil {
				return nil, err
			}
		}
		v.Body, err = instrumentBlock(v.Body, append(append([]string(nil), names...), v.Var.Val), line)
		return &v, err
	case *ast.ForInStat:
		v := *n
		for i, e := range v.Params {
			v.Params[i], err = instrumentExp(e, names, line)
			if err != nil {
				return nil, err
			}
		}
		locals := append([]string(nil), names...)
		for _, x := range v.Vars {
			locals = append(locals, x.Val)
		}
		v.Body, err = instrumentBlock(v.Body, locals, line)
		return &v, err
	default:
		return stat, nil
	}
}

func instrumentExp(exp ast.ExpNode, names []string, line int) (ast.ExpNode, error) {
	var err error
	switch n := exp.(type) {
	case ast.Function:
		return instrumentFunction(n, names, line)
	case ast.FunctionCall:
		b := *n.BFunctionCall
		b.Target, err = instrumentExp(b.Target, names, line)
		if err != nil {
			return nil, err
		}
		for i, e := range b.Args {
			b.Args[i], err = instrumentExp(e, names, line)
			if err != nil {
				return nil, err
			}
		}
		n.BFunctionCall = &b
		return n, nil
	case *ast.BFunctionCall:
		b := *n
		b.Target, err = instrumentExp(b.Target, names, line)
		if err != nil {
			return nil, err
		}
		for i, e := range b.Args {
			b.Args[i], err = instrumentExp(e, names, line)
			if err != nil {
				return nil, err
			}
		}
		return &b, nil
	case ast.TableConstructor:
		for i, f := range n.Fields {
			f.Value, err = instrumentExp(f.Value, names, line)
			if err != nil {
				return nil, err
			}
			n.Fields[i] = f
		}
		return n, nil
	case ast.IndexExp:
		n.Coll, err = instrumentExp(n.Coll, names, line)
		if err != nil {
			return nil, err
		}
		n.Idx, err = instrumentExp(n.Idx, names, line)
		return n, err
	case *ast.BinOp:
		v := *n
		v.Left, err = instrumentExp(v.Left, names, line)
		if err != nil {
			return nil, err
		}
		for i, o := range v.Right {
			o.Operand, err = instrumentExp(o.Operand, names, line)
			if err != nil {
				return nil, err
			}
			v.Right[i] = o
		}
		return &v, nil
	case *ast.UnOp:
		v := *n
		v.Operand, err = instrumentExp(v.Operand, names, line)
		return &v, err
	default:
		return exp, nil
	}
}
