package protocol

// castHasPosition is the original v148 predicate 0x6AC385: the skill-use
// sender (0x932141) writes the caster's X/Y right after the level only for
// these skills. Ported branch by branch from the client so the parser reads
// the same bytes the client sent.
func castHasPosition(a int) bool {
	if a == 5301003 {
		return true
	}
	if a > 5301003 {
		if a == 15111005 {
			return true
		}
		if a > 15111005 {
			if a == 24121004 {
				return true
			}
			if a > 24121004 {
				if a == 33121007 {
					return true
				}
				if a > 33121007 {
					if a == 35111013 || a == 51101004 {
						return true
					}
					if a > 51111003 {
						return a <= 51111005 || a == 51121005
					}
					return false
				}
				if a < 24121007 {
					return false
				}
				if a <= 24121008 || a == 31111004 {
					return true
				}
				if a <= 31121002 {
					return false
				}
				return a <= 31121005 || a == 32111004 || a == 32121007
			}
			if a == 22171000 || a == 15111011 {
				return true
			}
			if a > 22171000 {
				if a == 22181000 || a == 23111004 {
					return true
				}
				if a > 23121003 {
					return a <= 23121005 || a == 24111003
				}
				return false
			}
			if a <= 20031208 {
				return false
			}
			return a <= 20031210 || a == 21111012 || a == 21121000 || a == 22161004
		}
		if a == 5821000 {
			return true
		}
		if a > 5821000 {
			if a > 11111008 {
				if a < 12101000 {
					return false
				}
				return a <= 12101001 || a == 14001003 || a == 14001007 || a == 15101008
			}
			switch a {
			case 11111008, 5821009, 5911007, 5921000, 11001001, 11101003:
				return true
			}
			return false
		}
		if a == 5711011 {
			return true
		}
		if a > 5711011 {
			return a == 5720005 || a == 5721000 || a == 5721009 || a == 5811007
		}
		if a < 5311004 {
			return false
		}
		if a <= 5311005 {
			return true
		}
		if a <= 5320006 {
			return false
		}
		return a <= 5320008 || a == 5321005 || a == 5701006 || a == 5711007
	}
	if a == 3121000 {
		return true
	}
	if a > 3121000 {
		if a == 5101011 {
			return true
		}
		if a > 5101011 {
			if a > 5211007 {
				d := a - 5211009
				return d == 0 || d == 9005 || d == 9991 || d == 10009
			}
			if a == 5211007 {
				return true
			}
			d := a - 5111007
			return d == 0 || d == 9005 || d == 9993 || d == 10002 || d == 10008
		}
		if a > 4121014 {
			d := a - 4221000
			return d == 0 || d == 80003 || d == 120000 || d == 120007
		}
		switch a {
		case 4121014, 3121002, 3221000, 4001003, 4001005, 4121000:
			return true
		}
		return false
	}
	if a == 2101001 {
		return true
	}
	if a < 2101001 {
		if a <= 1211010 {
			if a >= 1211009 {
				return true
			}
			switch a {
			case 1001003, 1101006, 1111007, 1121000, 1201006:
				return true
			}
			return false
		}
		if a == 1221000 {
			return true
		}
		if a > 1301005 {
			return a <= 1301007 || a == 1311007 || a == 1321000
		}
		return false
	}
	if a > 2301004 {
		return a == 2311001 || a == 2311003 || a == 2321000 || a == 2321005
	}
	switch a {
	case 2301004, 2101003, 2121000, 2201001, 2201003, 2221000:
		return true
	}
	return false
}
