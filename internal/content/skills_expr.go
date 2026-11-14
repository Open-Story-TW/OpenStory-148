package content

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// WZ formulas are parsed as bounded arithmetic, never executed as source code.
// x is skill level, u() and d() are the original ceil/floor functions.
func SkillFormula(source string, level int) (int, error) {
	if len(source) == 0 || len(source) > 256 || level < 0 || level > 255 {
		return 0, fmt.Errorf("invalid skill formula")
	}
	p := skillExpr{text: strings.TrimPrefix(strings.ReplaceAll(source, " ", ""), "="), level: float64(level)}
	v, e := p.expression(0)
	if e != nil || p.at != len(p.text) || math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 2147483647 {
		return 0, fmt.Errorf("invalid skill formula %q", source)
	}
	return int(v), nil
}

type skillExpr struct {
	text    string
	at, ops int
	level   float64
}

func (p *skillExpr) expression(depth int) (float64, error) {
	v, e := p.product(depth + 1)
	if e != nil {
		return 0, e
	}
	for p.at < len(p.text) && (p.text[p.at] == '+' || p.text[p.at] == '-') {
		op := p.text[p.at]
		p.at++
		b, e := p.product(depth + 1)
		if e != nil {
			return 0, e
		}
		if op == '+' {
			v += b
		} else {
			v -= b
		}
	}
	return v, nil
}
func (p *skillExpr) product(depth int) (float64, error) {
	v, e := p.atom(depth + 1)
	if e != nil {
		return 0, e
	}
	for p.at < len(p.text) && (p.text[p.at] == '*' || p.text[p.at] == '/') {
		op := p.text[p.at]
		p.at++
		b, e := p.atom(depth + 1)
		if e != nil {
			return 0, e
		}
		if op == '*' {
			v *= b
		} else {
			if b == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			v /= b
		}
	}
	return v, nil
}
func (p *skillExpr) atom(depth int) (float64, error) {
	p.ops++
	if depth > 32 || p.ops > 128 || p.at >= len(p.text) {
		return 0, fmt.Errorf("formula limit")
	}
	c := p.text[p.at]
	p.at++
	if c == '+' || c == '-' {
		v, e := p.atom(depth + 1)
		if c == '-' {
			v = -v
		}
		return v, e
	}
	if c == 'x' {
		return p.level, nil
	}
	if c == '(' || c == 'u' || c == 'd' {
		if c != '(' {
			if p.at >= len(p.text) || p.text[p.at] != '(' {
				return 0, fmt.Errorf("function needs parenthesis")
			}
			p.at++
		}
		v, e := p.expression(depth + 1)
		if e != nil || p.at >= len(p.text) || p.text[p.at] != ')' {
			return 0, fmt.Errorf("unbalanced formula")
		}
		p.at++
		if c == 'u' {
			v = math.Ceil(v)
		} else if c == 'd' {
			v = math.Floor(v)
		}
		return v, nil
	}
	if unicode.IsDigit(rune(c)) || c == '.' {
		start := p.at - 1
		for p.at < len(p.text) && (p.text[p.at] >= '0' && p.text[p.at] <= '9' || p.text[p.at] == '.') {
			p.at++
		}
		return strconv.ParseFloat(p.text[start:p.at], 64)
	}
	return 0, fmt.Errorf("unsupported token")
}
