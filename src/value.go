package main

import (
	"encoding/json"
	"fmt"
	"strconv"
)

type Kind byte

const (
	KNull Kind = iota
	KInt
	KFloat
	KBool
	KStr
	KArr
	KObj
)

type Value struct {
	kind Kind
	i    int64
	f    float64
	b    bool
	s    string
	a    []Value
	o    map[string]Value
}

type Row = map[string]Value

func NullValue() Value                  { return Value{kind: KNull} }
func IntValue(i int64) Value            { return Value{kind: KInt, i: i} }
func FloatValue(f float64) Value        { return Value{kind: KFloat, f: f} }
func BoolValue(b bool) Value            { return Value{kind: KBool, b: b} }
func StrValue(s string) Value           { return Value{kind: KStr, s: s} }
func ArrValue(a []Value) Value          { return Value{kind: KArr, a: a} }
func ObjValue(o map[string]Value) Value { return Value{kind: KObj, o: o} }

func (this Value) TypeName() string {
	switch this.kind {
	case KNull:
		return "null"
	case KInt:
		return "int"
	case KFloat:
		return "float"
	case KBool:
		return "bool"
	case KStr:
		return "string"
	case KArr:
		return "array"
	case KObj:
		return "object"
	}
	return "?"
}

func ValueEqual(a Value, b Value) bool {
	if a.kind == KInt && b.kind == KFloat {
		return float64(a.i) == b.f
	}
	if a.kind == KFloat && b.kind == KInt {
		return a.f == float64(b.i)
	}
	if a.kind != b.kind {
		return false
	}
	switch a.kind {
	case KNull:
		return true
	case KInt:
		return a.i == b.i
	case KFloat:
		return a.f == b.f
	case KBool:
		return a.b == b.b
	case KStr:
		return a.s == b.s
	}
	return false
}

func numberCompare(a Value, b Value) int {
	var fa float64
	var fb float64
	if a.kind == KInt {
		fa = float64(a.i)
	} else {
		fa = a.f
	}
	if b.kind == KInt {
		fb = float64(b.i)
	} else {
		fb = b.f
	}
	if fa < fb {
		return -1
	}
	if fa > fb {
		return 1
	}
	return 0
}

func stringify(v Value) string {
	switch v.kind {
	case KInt:
		return strconv.FormatInt(v.i, 10)
	case KFloat:
		return strconv.FormatFloat(v.f, 'g', -1, 64)
	case KBool:
		if v.b {
			return "true"
		}
		return "false"
	case KStr:
		return v.s
	}
	return ""
}

func parseLiteral(s string) Value {
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return IntValue(i)
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return FloatValue(f)
	}
	return StrValue(s)
}

func parseAddValue(s string) Value {
	i, err := strconv.Atoi(s)
	if err != nil {
		return StrValue(s)
	}
	return IntValue(int64(i))
}

func parseOp(s string) (string, bool) {
	switch s {
	case ">", ">=", "<", "<=", "==", "!=":
		return s, true
	}
	return "", false
}

func orderable(v Value) bool {
	return v.kind == KNull || v.kind == KInt || v.kind == KFloat
}

func orderErr(a Value, b Value) error {
	return fmt.Errorf("Runtime Error: cannot order %s against %s", a.TypeName(), b.TypeName())
}

func evalCondErr(left Value, op string, right Value) (bool, error) {
	switch op {
	case "==":
		return ValueEqual(left, right), nil
	case "!=":
		return !ValueEqual(left, right), nil
	}
	if !orderable(left) {
		return false, orderErr(left, right)
	}
	if !orderable(right) {
		return false, orderErr(left, right)
	}
	var c int
	if left.kind == KNull && right.kind == KNull {
		c = 0
	} else if left.kind == KNull {
		c = -1
	} else if right.kind == KNull {
		c = 1
	} else {
		c = numberCompare(left, right)
	}
	switch op {
	case ">":
		return c > 0, nil
	case ">=":
		return c >= 0, nil
	case "<":
		return c < 0, nil
	case "<=":
		return c <= 0, nil
	}
	return false, nil
}

func renderRows(rows []Row) string {
	var conv func(Value) interface{}
	conv = func(v Value) interface{} {
		switch v.kind {
		case KNull:
			return nil
		case KInt:
			return v.i
		case KFloat:
			return v.f
		case KBool:
			return v.b
		case KStr:
			return v.s
		case KArr:
			out := make([]interface{}, 0, len(v.a))
			for _, item := range v.a {
				out = append(out, conv(item))
			}
			return out
		case KObj:
			out := make(map[string]interface{}, len(v.o))
			for k, item := range v.o {
				out[k] = conv(item)
			}
			return out
		}
		return nil
	}
	list := make([]interface{}, 0, len(rows))
	for _, r := range rows {
		m := make(map[string]interface{}, len(r))
		for k, v := range r {
			m[k] = conv(v)
		}
		list = append(list, m)
	}
	data, err := json.MarshalIndent(list, "", "    ")
	if err != nil {
		return "[]"
	}
	return string(data)
}
