package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type exitSignal struct{}

func (this exitSignal) Error() string { return "exit" }

var errExit = exitSignal{}

func runtimeError(format string, args ...interface{}) error {
	return fmt.Errorf("Runtime Error: %s", fmt.Sprintf(format, args...))
}

var dataCommands = map[string]bool{
	"load": true, "use": true, "only": true, "delete": true, "sort": true,
	"take": true, "add": true, "update": true, "show": true, "save": true,
	"clear": true, "drop": true, "rename": true, "unique": true, "reverse": true,
	"join": true,
}

type txSnapshot struct {
	doc    Value
	table  []Row
	single *Value
	vars   map[string]Value
}

type Interpreter struct {
	out          io.Writer
	baseDir      string
	printOn      bool
	key          string
	doc          Value
	table        []Row
	single       *Value
	vars         map[string]Value
	indexes      map[string]map[string][]int
	procs        map[string][]Line
	txStack      []txSnapshot
	includeDepth int
	lastCmd      string
}

func New(out io.Writer, printOn bool) *Interpreter {
	return &Interpreter{
		out:     out,
		printOn: printOn,
		vars:    map[string]Value{},
		indexes: map[string]map[string][]int{},
		procs:   map[string][]Line{},
	}
}

func (this *Interpreter) SetBaseDir(dir string) { this.baseDir = dir }
func (this *Interpreter) SetPrint(on bool)      { this.printOn = on }
func (this *Interpreter) SetKey(k string)       { this.key = k }
func (this *Interpreter) Key() string           { return this.key }
func (this *Interpreter) LastCommand() string   { return this.lastCmd }
func (this *Interpreter) RowCount() int         { return len(this.table) }

func (this *Interpreter) RunSource(src string) error {
	if errs := Validate(src); len(errs) > 0 {
		msgs := make([]string, 0, len(errs))
		for _, e := range errs {
			msgs = append(msgs, e.Error())
		}
		return fmt.Errorf("%s", strings.Join(msgs, "; "))
	}
	lines, err := Parse(src)
	if err != nil {
		return err
	}
	base := len(this.txStack)
	err = this.execLines(lines)
	if err != nil && err != errExit && len(this.txStack) > base {
		this.restoreTx(this.txStack[base])
		this.txStack = this.txStack[:base]
	}
	return err
}

func copyValue(v Value) Value {
	switch v.kind {
	case KArr:
		out := make([]Value, len(v.a))
		for i := range v.a {
			out[i] = copyValue(v.a[i])
		}
		return ArrValue(out)
	case KObj:
		out := make(map[string]Value, len(v.o))
		for k, item := range v.o {
			out[k] = copyValue(item)
		}
		return ObjValue(out)
	}
	return v
}

func (this *Interpreter) restoreTx(s txSnapshot) {
	this.doc = s.doc
	this.table = s.table
	this.single = s.single
	this.vars = s.vars
	this.clearIndexes()
}

func (this *Interpreter) clearIndexes() {
	this.indexes = map[string]map[string][]int{}
}

func (this *Interpreter) RunFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("Runtime Error: %s", err.Error())
	}
	this.baseDir = filepath.Dir(path)
	return this.RunSource(string(data))
}

func matchingEnd(lines []Line, start int) int {
	depth := 0
	for i := start; i < len(lines); i++ {
		depth += lines[i].blockDelta()
		if depth == 0 {
			return i
		}
	}
	return len(lines)
}

func (this *Interpreter) execLines(lines []Line) error {
	for i := 0; i < len(lines); i++ {
		ln := lines[i]
		switch ln.cmd {
		case "while", "for":
			end := matchingEnd(lines, i)
			if err := this.execLoop(ln, lines[i+1:end]); err != nil {
				return err
			}
			i = end
		case "if":
			elseAt := 0
			hasElse := false
			depth := 0
			end := i
			for end < len(lines) {
				depth += lines[end].blockDelta()
				if depth == 0 {
					break
				}
				if lines[end].cmd == "else" && depth == 1 {
					elseAt = end
					hasElse = true
				}
				end++
			}
			held, err := this.condHolds("if", ln.args)
			if err != nil {
				return err
			}
			if held {
				bodyEnd := end
				if hasElse {
					bodyEnd = elseAt
				}
				if err := this.execLines(lines[i+1 : bodyEnd]); err != nil {
					return err
				}
			} else if hasElse {
				if err := this.execLines(lines[elseAt+1 : end]); err != nil {
					return err
				}
			}
			i = end
		case "proc":
			if len(ln.args) != 1 {
				return runtimeError("proc requires a name, got %d argument(s)", len(ln.args))
			}
			end := matchingEnd(lines, i)
			this.procs[ln.args[0]] = lines[i+1 : end]
			i = end
		default:
			if err := this.execLine(ln); err != nil {
				return err
			}
		}
	}
	return nil
}

func (this *Interpreter) execLoop(header Line, body []Line) error {
	if header.cmd == "while" {
		for {
			held, err := this.condHolds("while", header.args)
			if err != nil {
				return err
			}
			if !held {
				return nil
			}
			if err := this.execLines(body); err != nil {
				return err
			}
		}
	}
	if err := argCount("for", header.args, 3, 4); err != nil {
		return err
	}
	name := header.args[0]
	start, err := this.argInt(header.args[1], "for")
	if err != nil {
		return err
	}
	limit, err := this.argInt(header.args[2], "for")
	if err != nil {
		return err
	}
	step := int64(1)
	if len(header.args) == 4 {
		step, err = this.argInt(header.args[3], "for")
		if err != nil {
			return err
		}
		if step == 0 {
			return runtimeError("for step cannot be zero")
		}
	}
	for i := start; ; i += step {
		if step > 0 && i >= limit {
			return nil
		}
		if step < 0 && i <= limit {
			return nil
		}
		this.vars[name] = IntValue(i)
		if err := this.execLines(body); err != nil {
			return err
		}
	}
}

func argCount(cmd string, args []string, need ...int) error {
	for _, n := range need {
		if len(args) == n {
			return nil
		}
	}
	joined := make([]string, 0, len(need))
	for _, n := range need {
		joined = append(joined, strconv.Itoa(n))
	}
	return runtimeError("%s requires %s argument(s), got %d", cmd, strings.Join(joined, " or "), len(args))
}

func (this *Interpreter) argInt(s string, cmd string) (int64, error) {
	i, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, runtimeError("%s requires an integer, got %q", cmd, s)
	}
	return i, nil
}

func (this *Interpreter) condHolds(cmd string, args []string) (bool, error) {
	if err := argCount(cmd, args, 3); err != nil {
		return false, err
	}
	op, ok := parseOp(args[1])
	if !ok {
		return false, runtimeError("invalid operator %q", args[1])
	}
	left, exists := this.vars[args[0]]
	if !exists {
		left = NullValue()
	}
	return evalCondErr(left, op, parseLiteral(args[2]))
}

func (this *Interpreter) execLine(ln Line) error {
	this.lastCmd = ln.cmd
	switch ln.cmd {
	case "load":
		return this.cmdLoad(ln.args)
	case "use":
		return this.cmdUse(ln.args)
	case "only":
		return this.cmdOnly(ln.args, false)
	case "delete":
		return this.cmdOnly(ln.args, true)
	case "sort":
		return this.cmdSort(ln.args)
	case "take":
		return this.cmdTake(ln.args)
	case "add":
		return this.cmdAdd(ln.args)
	case "update":
		return this.cmdUpdate(ln.args)
	case "save":
		return this.cmdSave(ln.args)
	case "show":
		return this.cmdShow(ln.args)
	case "print":
		return this.cmdPrint()
	case "printvar":
		return this.cmdPrintVar(ln.args)
	case "set":
		return this.cmdSet(ln.args)
	case "clear":
		return this.cmdClear()
	case "drop":
		return this.cmdDrop(ln.args)
	case "rename":
		return this.cmdRename(ln.args)
	case "unique":
		return this.cmdUnique(ln.args)
	case "reverse":
		return this.cmdReverse()
	case "echo":
		return this.cmdEcho(ln.args)
	case "key":
		return this.cmdKey(ln.args)
	case "join":
		return this.cmdJoin(ln.args)
	case "index":
		return this.cmdIndex(ln.args)
	case "begin":
		return this.cmdBegin()
	case "commit":
		return this.cmdCommit()
	case "rollback":
		return this.cmdRollback()
	case "call":
		return this.cmdCall(ln.args)
	case "include":
		return this.cmdInclude(ln.args)
	case "hash":
		return this.cmdHash(ln.args)
	case "exit":
		return errExit
	case "sleep":
		return this.cmdSleep(ln.args)
	case "++", "--":
		return this.cmdIncDec(ln.cmd, ln.args)
	case "+", "-", "*", "/":
		return this.cmdMath(ln.cmd, ln.args)
	case "if", "else", "end", "proc":
		return runtimeError("unexpected %s", ln.cmd)
	}
	return runtimeError("unknown command: %s", ln.cmd)
}

func (this *Interpreter) resolvePath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	if this.baseDir == "" {
		return p
	}
	return filepath.Join(this.baseDir, p)
}

func dbTarget(name string) string {
	if strings.HasSuffix(name, ".dcdb") {
		return name
	}
	return filepath.Join("data", name+".dcdb")
}

func (this *Interpreter) cmdLoad(args []string) error {
	if err := argCount("load", args, 1); err != nil {
		return err
	}
	path := this.resolvePath(dbTarget(args[0]))
	doc, err := LoadDoc(path, this.key)
	if err != nil {
		return runtimeError("%s", err.Error())
	}
	this.doc = doc
	this.table = nil
	this.single = nil
	this.vars["count"] = IntValue(0)
	this.clearIndexes()
	if this.doc.kind == KArr {
		rows, single := makeTable(this.doc)
		this.table = rows
		this.single = single
		this.vars["count"] = IntValue(int64(len(this.table)))
	}
	return nil
}

func (this *Interpreter) cmdUse(args []string) error {
	if err := argCount("use", args, 1); err != nil {
		return err
	}
	full := args[0]
	cur := this.doc
	parts := strings.Split(full, ".")
	for _, key := range parts {
		if cur.kind != KObj {
			return runtimeError("path not found: %s", full)
		}
		next, ok := cur.o[key]
		if !ok {
			return runtimeError("path not found: %s", full)
		}
		cur = next
	}
	rows, single := makeTable(cur)
	this.table = rows
	this.single = single
	this.vars["count"] = IntValue(int64(len(this.table)))
	this.clearIndexes()
	return nil
}

func makeTable(v Value) ([]Row, *Value) {
	switch v.kind {
	case KObj:
		keys := make([]string, 0, len(v.o))
		for k := range v.o {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if len(keys) > 0 && v.o[keys[0]].kind == KObj {
			rows := make([]Row, 0, len(keys))
			for _, k := range keys {
				r := make(Row, len(v.o[k].o)+1)
				for f, val := range v.o[k].o {
					r[f] = val
				}
				r["_id"] = StrValue(k)
				rows = append(rows, r)
			}
			return rows, nil
		}
		rows := make([]Row, 0, len(keys))
		for _, k := range keys {
			r := make(Row, 1)
			r["value"] = v.o[k]
			rows = append(rows, r)
		}
		return rows, nil
	case KArr:
		if len(v.a) == 0 {
			return nil, nil
		}
		if v.a[0].kind == KObj {
			rows := make([]Row, 0, len(v.a))
			for _, item := range v.a {
				rows = append(rows, item.o)
			}
			return rows, nil
		}
		return nil, &v
	}
	return nil, &v
}

func (this *Interpreter) cmdOnly(args []string, negate bool) error {
	if err := argCount("only", args, 3); err != nil {
		return err
	}
	op, ok := parseOp(args[1])
	if !ok {
		return runtimeError("invalid operator %q", args[1])
	}
	field := args[0]
	right := parseLiteral(args[2])
	if op != "==" && op != "!=" {
		if !orderable(right) {
			return runtimeError("cannot order values against %s", right.TypeName())
		}
		for _, r := range this.table {
			left, exists := r[field]
			if !exists {
				left = NullValue()
			}
			if !orderable(left) {
				return orderErr(left, right)
			}
		}
	}
	if idx, ok := this.indexes[field]; ok && op == "==" && !negate {
		offsets := idx[stringify(right)]
		out := make([]Row, 0, len(offsets))
		for _, o := range offsets {
			out = append(out, this.table[o])
		}
		this.table = out
		this.vars["count"] = IntValue(int64(len(out)))
		this.clearIndexes()
		return nil
	}
	n := len(this.table)
	flags := make([]bool, n)
	w := 1
	if n >= 1024 {
		w = workCount(n)
	}
	if w == 1 {
		for i := 0; i < n; i++ {
			left, ok := this.table[i][field]
			if !ok {
				left = NullValue()
			}
			keep, _ := evalCondErr(left, op, right)
			flags[i] = keep != negate
		}
	} else {
		var wg sync.WaitGroup
		for _, span := range splitRanges(n, w) {
			s := span[0]
			e := span[1]
			wg.Add(1)
			go func(s int, e int) {
				defer wg.Done()
				for i := s; i < e; i++ {
					left, ok := this.table[i][field]
					if !ok {
						left = NullValue()
					}
					keep, _ := evalCondErr(left, op, right)
					flags[i] = keep != negate
				}
			}(s, e)
		}
		wg.Wait()
	}
	out := make([]Row, 0, n)
	for i := 0; i < n; i++ {
		if flags[i] {
			out = append(out, this.table[i])
		}
	}
	this.table = out
	this.vars["count"] = IntValue(int64(len(out)))
	this.clearIndexes()
	return nil
}

func (this *Interpreter) cmdSort(args []string) error {
	if err := argCount("sort", args, 1, 2); err != nil {
		return err
	}
	field := args[0]
	desc := true
	if len(args) == 2 && args[1] == "asc" {
		desc = false
	}
	sort.SliceStable(this.table, func(i, j int) bool {
		left, lok := this.table[i][field]
		if !lok {
			left = NullValue()
		}
		right, rok := this.table[j][field]
		if !rok {
			right = NullValue()
		}
		var c int
		leftNum := left.kind == KInt || left.kind == KFloat
		rightNum := right.kind == KInt || right.kind == KFloat
		switch {
		case leftNum && rightNum:
			c = numberCompare(left, right)
		case left.kind == KNull && right.kind == KNull:
			c = 0
		case left.kind == KNull:
			c = -1
		case right.kind == KNull:
			c = 1
		default:
			sa := stringify(left)
			sb := stringify(right)
			if sa < sb {
				c = -1
			} else if sa > sb {
				c = 1
			} else {
				c = 0
			}
		}
		if desc {
			return c > 0
		}
		return c < 0
	})
	this.clearIndexes()
	return nil
}

func (this *Interpreter) cmdTake(args []string) error {
	if err := argCount("take", args, 1); err != nil {
		return err
	}
	n, err := this.argInt(args[0], "take")
	if err != nil {
		return err
	}
	if n < 0 {
		n = 0
	}
	if n > int64(len(this.table)) {
		n = int64(len(this.table))
	}
	this.table = this.table[:n]
	this.vars["count"] = IntValue(n)
	this.clearIndexes()
	return nil
}

func (this *Interpreter) cmdAdd(args []string) error {
	if len(args) == 0 {
		return runtimeError("add requires at least one field=value pair")
	}
	row := make(Row, len(args))
	for _, pair := range args {
		eq := strings.IndexByte(pair, '=')
		if eq <= 0 {
			return runtimeError("invalid field=value %q", pair)
		}
		row[pair[:eq]] = parseAddValue(pair[eq+1:])
	}
	this.table = append(this.table, row)
	for f, idx := range this.indexes {
		if v, ok := row[f]; ok {
			idx[stringify(v)] = append(idx[stringify(v)], len(this.table)-1)
		} else {
			idx[stringify(NullValue())] = append(idx[stringify(NullValue())], len(this.table)-1)
		}
	}
	this.vars["count"] = IntValue(int64(len(this.table)))
	return nil
}

func (this *Interpreter) cmdUpdate(args []string) error {
	if err := argCount("update", args, 4); err != nil {
		return err
	}
	field := args[0]
	value := copyValue(parseAddValue(args[1]))
	whereField := args[2]
	whereValue := parseAddValue(args[3])
	if idx, ok := this.indexes[whereField]; ok {
		for _, o := range idx[stringify(whereValue)] {
			this.table[o][field] = copyValue(value)
		}
		this.clearIndexes()
		return nil
	}
	this.table = parallelApply(this.table, func(i int, r Row) Row {
		current, ok := r[whereField]
		if !ok {
			current = NullValue()
		}
		if stringify(current) == stringify(whereValue) {
			r[field] = copyValue(value)
		}
		return r
	})
	this.clearIndexes()
	return nil
}

func (this *Interpreter) cmdSave(args []string) error {
	if err := argCount("save", args, 1); err != nil {
		return err
	}
	path := this.resolvePath(dbTarget(args[0]))
	rows := this.table
	if this.single != nil {
		rows = []Row{{"value": *this.single}}
	}
	doc := makeDoc(rows)
	return SaveDoc(this.resolvePath(path), doc, this.key)
}

func makeDoc(rows []Row) Value {
	arr := make([]Value, 0, len(rows))
	for _, r := range rows {
		arr = append(arr, ObjValue(r))
	}
	return ArrValue(arr)
}

func (this *Interpreter) cmdShow(args []string) error {
	if this.single != nil {
		row := make(Row, 1)
		row["value"] = *this.single
		this.table = []Row{row}
		this.single = nil
		this.vars["count"] = IntValue(1)
		return nil
	}
	if len(args) == 0 {
		this.clearIndexes()
		return nil
	}
	keep := map[string]bool{}
	for _, f := range args {
		keep[f] = true
	}
	this.table = parallelApply(this.table, func(i int, r Row) Row {
		out := make(Row, len(r))
		for k, v := range r {
			if keep[k] {
				out[k] = v
			}
		}
		return out
	})
	this.clearIndexes()
	return nil
}

func (this *Interpreter) cmdPrint() error {
	if !this.printOn {
		return nil
	}
	if this.single != nil {
		row := make(Row, 1)
		row["value"] = *this.single
		fmt.Fprintln(this.out, renderRows([]Row{row}))
		return nil
	}
	fmt.Fprintln(this.out, renderRows(this.table))
	return nil
}

func (this *Interpreter) cmdPrintVar(args []string) error {
	if err := argCount("printvar", args, 1); err != nil {
		return err
	}
	if !this.printOn {
		return nil
	}
	v, ok := this.vars[args[0]]
	if !ok {
		return runtimeError("no such variable %q", args[0])
	}
	fmt.Fprintln(this.out, stringify(v))
	return nil
}

func (this *Interpreter) cmdSet(args []string) error {
	if err := argCount("set", args, 2); err != nil {
		return err
	}
	this.vars[args[0]] = parseAddValue(args[1])
	return nil
}

func (this *Interpreter) cmdClear() error {
	this.table = nil
	this.single = nil
	this.vars["count"] = IntValue(0)
	this.clearIndexes()
	return nil
}

func (this *Interpreter) cmdDrop(args []string) error {
	if len(args) == 0 {
		return runtimeError("drop requires at least one field")
	}
	rm := map[string]bool{}
	for _, f := range args {
		rm[f] = true
	}
	this.table = parallelApply(this.table, func(i int, r Row) Row {
		out := make(Row, len(r))
		for k, v := range r {
			if !rm[k] {
				out[k] = v
			}
		}
		return out
	})
	this.clearIndexes()
	return nil
}

func (this *Interpreter) cmdRename(args []string) error {
	if err := argCount("rename", args, 2); err != nil {
		return err
	}
	oldName := args[0]
	newName := args[1]
	this.table = parallelApply(this.table, func(i int, r Row) Row {
		v, ok := r[oldName]
		if !ok {
			return r
		}
		out := make(Row, len(r))
		for k, val := range r {
			out[k] = val
		}
		delete(out, oldName)
		out[newName] = v
		return out
	})
	this.clearIndexes()
	return nil
}

func (this *Interpreter) cmdUnique(args []string) error {
	if err := argCount("unique", args, 1); err != nil {
		return err
	}
	field := args[0]
	seen := map[string]bool{}
	out := make([]Row, 0, len(this.table))
	for _, r := range this.table {
		v, ok := r[field]
		if !ok {
			v = NullValue()
		}
		k := stringify(v)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, r)
	}
	this.table = out
	this.vars["count"] = IntValue(int64(len(out)))
	this.clearIndexes()
	return nil
}

func (this *Interpreter) cmdReverse() error {
	for i, j := 0, len(this.table)-1; i < j; i, j = i+1, j-1 {
		this.table[i], this.table[j] = this.table[j], this.table[i]
	}
	this.clearIndexes()
	return nil
}

func (this *Interpreter) cmdEcho(args []string) error {
	fmt.Fprintln(this.out, strings.Join(args, " "))
	return nil
}

func (this *Interpreter) cmdKey(args []string) error {
	if err := argCount("key", args, 1); err != nil {
		return err
	}
	this.key = args[0]
	return nil
}

func (this *Interpreter) cmdJoin(args []string) error {
	leftMode := false
	rest := args
	if len(rest) > 0 && rest[0] == "left" {
		leftMode = true
		rest = rest[1:]
	}
	if err := argCount("join", rest, 4); err != nil {
		return err
	}
	if rest[1] != "on" {
		return runtimeError("join requires \"on\", got %q", rest[1])
	}
	path := this.resolvePath(dbTarget(rest[0]))
	doc, err := LoadDoc(path, this.key)
	if err != nil {
		return runtimeError("%s", err.Error())
	}
	rightRows, _ := makeTable(doc)
	byKey := map[string][]Row{}
	for _, r := range rightRows {
		v, ok := r[rest[3]]
		if !ok {
			v = NullValue()
		}
		k := stringify(v)
		byKey[k] = append(byKey[k], r)
	}
	out := make([]Row, 0, len(this.table))
	for _, l := range this.table {
		lv, ok := l[rest[2]]
		if !ok {
			lv = NullValue()
		}
		matches := byKey[stringify(lv)]
		if len(matches) == 0 {
			if leftMode {
				out = append(out, l)
			}
			continue
		}
		for _, r := range matches {
			merged := make(Row, len(l)+len(r))
			for k, v := range l {
				merged[k] = copyValue(v)
			}
			for k, v := range r {
				if _, exists := merged[k]; !exists {
					merged[k] = copyValue(v)
				}
			}
			out = append(out, merged)
		}
	}
	this.table = out
	this.vars["count"] = IntValue(int64(len(out)))
	this.clearIndexes()
	return nil
}

func (this *Interpreter) cmdIndex(args []string) error {
	if len(args) == 0 {
		return runtimeError("index requires a field or \"drop\"")
	}
	if args[0] == "drop" {
		if len(args) == 1 {
			this.clearIndexes()
			return nil
		}
		for _, f := range args[1:] {
			delete(this.indexes, f)
		}
		return nil
	}
	if len(args) > 1 {
		return runtimeError("index takes one field, got %d argument(s)", len(args))
	}
	field := args[0]
	idx := map[string][]int{}
	for i, r := range this.table {
		v, ok := r[field]
		if !ok {
			v = NullValue()
		}
		k := stringify(v)
		idx[k] = append(idx[k], i)
	}
	this.indexes[field] = idx
	return nil
}

func (this *Interpreter) cmdBegin() error {
	var single *Value
	if this.single != nil {
		sv := copyValue(*this.single)
		single = &sv
	}
	table := make([]Row, len(this.table))
	for i, row := range this.table {
		out := make(Row, len(row))
		for k, v := range row {
			out[k] = copyValue(v)
		}
		table[i] = out
	}
	vars := make(map[string]Value, len(this.vars))
	for k, v := range this.vars {
		vars[k] = copyValue(v)
	}
	this.txStack = append(this.txStack, txSnapshot{
		doc:    copyValue(this.doc),
		table:  table,
		single: single,
		vars:   vars,
	})
	return nil
}

func (this *Interpreter) cmdCommit() error {
	if len(this.txStack) == 0 {
		return runtimeError("commit without begin")
	}
	this.txStack = this.txStack[:len(this.txStack)-1]
	this.clearIndexes()
	return nil
}

func (this *Interpreter) cmdRollback() error {
	if len(this.txStack) == 0 {
		return runtimeError("rollback without begin")
	}
	top := this.txStack[len(this.txStack)-1]
	this.txStack = this.txStack[:len(this.txStack)-1]
	this.restoreTx(top)
	return nil
}

func (this *Interpreter) cmdCall(args []string) error {
	if err := argCount("call", args, 1); err != nil {
		return err
	}
	body, ok := this.procs[args[0]]
	if !ok {
		return runtimeError("no such proc %q", args[0])
	}
	return this.execLines(body)
}

func (this *Interpreter) cmdInclude(args []string) error {
	if err := argCount("include", args, 1); err != nil {
		return err
	}
	if this.includeDepth >= 16 {
		return runtimeError("include depth exceeded")
	}
	name := args[0]
	if !strings.HasSuffix(name, ".dc") {
		name = name + ".dc"
	}
	data, err := os.ReadFile(this.resolvePath(name))
	if err != nil {
		return runtimeError("%s", err.Error())
	}
	this.includeDepth++
	err = this.RunSource(string(data))
	this.includeDepth--
	return err
}

func (this *Interpreter) cmdHash(args []string) error {
	sum := sha256.Sum256([]byte(strings.Join(args, " ")))
	fmt.Fprintf(this.out, "%x\n", sum)
	return nil
}

func (this *Interpreter) cmdSleep(args []string) error {
	if err := argCount("sleep", args, 1); err != nil {
		return err
	}
	ms, err := this.argInt(args[0], "sleep")
	if err != nil {
		return err
	}
	time.Sleep(time.Duration(ms) * time.Millisecond)
	return nil
}

func (this *Interpreter) cmdIncDec(cmd string, args []string) error {
	if err := argCount(cmd, args, 1); err != nil {
		return err
	}
	name := args[0]
	cur, ok := this.vars[name]
	if !ok || cur.kind != KInt {
		return runtimeError("%s requires a numeric variable, got %q", cmd, name)
	}
	if cmd == "++" {
		this.vars[name] = IntValue(cur.i + 1)
	} else {
		this.vars[name] = IntValue(cur.i - 1)
	}
	return nil
}

func (this *Interpreter) cmdMath(cmd string, args []string) error {
	if err := argCount(cmd, args, 2); err != nil {
		return err
	}
	name := args[0]
	cur, ok := this.vars[name]
	if !ok || cur.kind != KInt {
		return runtimeError("%s requires a numeric variable, got %q", cmd, name)
	}
	n, err := this.argInt(args[1], cmd)
	if err != nil {
		return err
	}
	switch cmd {
	case "+":
		this.vars[name] = IntValue(cur.i + n)
	case "-":
		this.vars[name] = IntValue(cur.i - n)
	case "*":
		this.vars[name] = IntValue(cur.i * n)
	case "/":
		if n == 0 {
			return nil
		}
		this.vars[name] = IntValue(cur.i / n)
	}
	return nil
}

func (this *Interpreter) PrintTable() {
	if this.single != nil {
		row := make(Row, 1)
		row["value"] = *this.single
		fmt.Fprintln(this.out, renderRows([]Row{row}))
		return
	}
	fmt.Fprintln(this.out, renderRows(this.table))
}
