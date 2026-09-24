package main

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runSrc(t *testing.T, src string) (*Interpreter, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	interp := New(buf, true)
	interp.SetBaseDir(t.TempDir())
	if err := interp.RunSource(src); err != nil {
		t.Fatalf("RunSource(%q): %v", src, err)
	}
	return interp, buf
}

func TestSaveLoadValueTypes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "types.dcdb")
	doc := ObjValue(map[string]Value{
		"n":    NullValue(),
		"i":    IntValue(-42),
		"f":    FloatValue(3.25),
		"b":    BoolValue(true),
		"s":    StrValue("hello world"),
		"list": ArrValue([]Value{IntValue(1), StrValue("two")}),
		"nested": ObjValue(map[string]Value{
			"deep": IntValue(7),
		}),
	})
	if err := SaveDoc(path, doc, "sekret"); err != nil {
		t.Fatalf("SaveDoc: %v", err)
	}
	loaded, err := LoadDoc(path, "sekret")
	if err != nil {
		t.Fatalf("LoadDoc: %v", err)
	}
	if loaded.kind != KObj {
		t.Fatalf("kind = %v, want KObj", loaded.kind)
	}
	if !ValueEqual(loaded.o["n"], NullValue()) {
		t.Error("null roundtrip failed")
	}
	if !ValueEqual(loaded.o["i"], IntValue(-42)) {
		t.Error("int roundtrip failed")
	}
	if !ValueEqual(loaded.o["f"], FloatValue(3.25)) {
		t.Error("float roundtrip failed")
	}
	if !ValueEqual(loaded.o["b"], BoolValue(true)) {
		t.Error("bool roundtrip failed")
	}
	if !ValueEqual(loaded.o["s"], StrValue("hello world")) {
		t.Error("string roundtrip failed")
	}
	if !ValueEqual(loaded.o["nested"].o["deep"], IntValue(7)) {
		t.Error("nested roundtrip failed")
	}
	if _, err := LoadDoc(path, "nope"); err == nil {
		t.Error("wrong key should fail to load")
	} else if !strings.Contains(err.Error(), "corrupt") {
		t.Errorf("wrong key error = %q", err.Error())
	}
	if _, err := LoadDoc(path, "sekret"); err != nil {
		t.Fatalf("reload with the right key: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("hello world")) || bytes.Contains(raw, []byte("nested")) {
		t.Error("plaintext leaked into database file")
	}
}

func TestQuickstartFlow(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "users.dcdb")
	interp := New(&bytes.Buffer{}, false)
	interp.SetBaseDir(dir)
	build := "add name=\"Alice\" city=\"Reno\" age=34\n" +
		"add name=\"Bob\" city=\"Provo\" age=17\n" +
		"add name=\"Cara\" city=\"Reno\" age=21\n" +
		"save " + dbPath + "\n"
	if err := interp.RunSource(build); err != nil {
		t.Fatalf("build: %v", err)
	}
	if n := interp.RowCount(); n != 3 {
		t.Fatalf("rows after add = %d, want 3", n)
	}
	src := "load " + dbPath + "\n" +
		"only age >= 18\n" +
		"sort age\n" +
		"take 10\n" +
		"show name city age\n"
	if err := interp.RunSource(src); err != nil {
		t.Fatalf("run: %v", err)
	}
	if n := interp.RowCount(); n != 2 {
		t.Fatalf("rows after only = %d, want 2", n)
	}
	if !ValueEqual(interp.table[0]["name"], StrValue("Alice")) {
		t.Errorf("row0 name = %v, want Alice", interp.table[0]["name"])
	}
	if !ValueEqual(interp.table[1]["name"], StrValue("Cara")) {
		t.Errorf("row1 name = %v, want Cara", interp.table[1]["name"])
	}
	if _, ok := interp.table[0]["city"]; !ok {
		t.Error("show dropped city")
	}
	if _, ok := interp.table[0]["_id"]; ok {
		t.Error("show kept unspecified field")
	}
	if c := interp.vars["count"]; c.i != 2 {
		t.Errorf("count = %d, want 2", c.i)
	}
}

func TestCountVariable(t *testing.T) {
	interp, _ := runSrc(t, "add n=1\nadd n=2\nadd n=3\n")
	if c := interp.vars["count"].i; c != 3 {
		t.Fatalf("count after add = %d, want 3", c)
	}
	if err := interp.RunSource("take 2\n"); err != nil {
		t.Fatal(err)
	}
	if c := interp.vars["count"].i; c != 2 {
		t.Fatalf("count after take = %d, want 2", c)
	}
	if err := interp.RunSource("only n >= 2\n"); err != nil {
		t.Fatal(err)
	}
	if c := interp.vars["count"].i; c != 1 {
		t.Fatalf("count after only = %d, want 1", c)
	}
}

func TestUsePaths(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "roots.dcdb")
	doc := ObjValue(map[string]Value{
		"users": ObjValue(map[string]Value{
			"alice": ObjValue(map[string]Value{"age": IntValue(34)}),
			"bob":   ObjValue(map[string]Value{"age": IntValue(17)}),
		}),
		"settings": ObjValue(map[string]Value{
			"retryLimit": IntValue(3),
		}),
	})
	if err := SaveDoc(path, doc, ""); err != nil {
		t.Fatal(err)
	}
	interp, _ := runSrc(t, "load "+path+"\nuse users\n")
	if n := interp.RowCount(); n != 2 {
		t.Fatalf("rows after use users = %d, want 2", n)
	}
	byID := map[string]Row{}
	for _, r := range interp.table {
		byID[r["_id"].s] = r
	}
	if !ValueEqual(byID["alice"]["age"], IntValue(34)) {
		t.Error("alice age wrong")
	}
	if !ValueEqual(byID["bob"]["age"], IntValue(17)) {
		t.Error("bob age wrong")
	}

	interp2 := New(&bytes.Buffer{}, false)
	interp2.SetBaseDir(dir)
	if err := interp2.RunSource("load " + path + "\nuse settings.retryLimit\nshow\n"); err != nil {
		t.Fatal(err)
	}
	if n := interp2.RowCount(); n != 1 {
		t.Fatalf("rows after scalar use = %d, want 1", n)
	}
	if !ValueEqual(interp2.table[0]["value"], IntValue(3)) {
		t.Errorf("scalar value = %v, want 3", interp2.table[0]["value"])
	}
}

func TestObjectOfScalars(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "flags.dcdb")
	doc := ObjValue(map[string]Value{
		"flags": ObjValue(map[string]Value{
			"beta":  BoolValue(true),
			"count": IntValue(5),
			"tag":   StrValue("x"),
		}),
	})
	if err := SaveDoc(path, doc, ""); err != nil {
		t.Fatal(err)
	}
	interp, _ := runSrc(t, "load "+path+"\nuse flags\n")
	if n := interp.RowCount(); n != 3 {
		t.Fatalf("rows = %d, want 3", n)
	}
	seen := map[string]bool{}
	for _, r := range interp.table {
		seen[stringify(r["value"])] = true
	}
	if !seen["true"] {
		t.Error("bool scalar missing")
	}
	if !seen["5"] {
		t.Error("int scalar missing")
	}
	if !seen["x"] {
		t.Error("string scalar missing")
	}
}

func TestLoops(t *testing.T) {
	interp, _ := runSrc(t, "set total 0\nfor i 0 5\n+ total 1\nend\n")
	if v := interp.vars["total"]; v.i != 5 {
		t.Fatalf("total = %d, want 5", v.i)
	}
	if err := interp.RunSource("set n 3\nwhile n > 0\n-- n\nend\n"); err != nil {
		t.Fatal(err)
	}
	if v := interp.vars["n"]; v.i != 0 {
		t.Fatalf("n = %d, want 0", v.i)
	}
	if err := interp.RunSource("set steps 0\nfor k 5 0 -2\n++ steps\nend\n"); err != nil {
		t.Fatal(err)
	}
	if v := interp.vars["steps"]; v.i != 3 {
		t.Fatalf("steps = %d, want 3 (5,3,1)", v.i)
	}
}

func TestIfElse(t *testing.T) {
	interp, _ := runSrc(t, "set x 5\nset y \"none\"\nif x > 3\nset y \"big\"\nelse\nset y \"small\"\nend\n")
	if !ValueEqual(interp.vars["y"], StrValue("big")) {
		t.Errorf("y = %v, want big", interp.vars["y"])
	}
	if err := interp.RunSource("set x 1\nset y \"none\"\nif x > 3\nset y \"big\"\nelse\nset y \"small\"\nend\n"); err != nil {
		t.Fatal(err)
	}
	if !ValueEqual(interp.vars["y"], StrValue("small")) {
		t.Errorf("y = %v, want small", interp.vars["y"])
	}
	if err := interp.RunSource("set x 2\nif x == 2\nset hit 1\nend\n"); err != nil {
		t.Fatal(err)
	}
	if !ValueEqual(interp.vars["hit"], IntValue(1)) {
		t.Error("else-less if did not run")
	}
}

func TestMoreCommands(t *testing.T) {
	interp, _ := runSrc(t,
		"add name=\"a\" tag=\"x\" n=1\n"+
			"add name=\"b\" tag=\"x\" n=2\n"+
			"add name=\"c\" tag=\"y\" n=3\n"+
			"unique tag\n")
	if interp.RowCount() != 2 {
		t.Fatalf("unique kept %d rows, want 2", interp.RowCount())
	}
	if !ValueEqual(interp.table[0]["tag"], StrValue("x")) {
		t.Error("unique lost first occurrence")
	}
	if err := interp.RunSource("reverse\n"); err != nil {
		t.Fatal(err)
	}
	if !ValueEqual(interp.table[0]["n"], IntValue(3)) {
		t.Error("reverse order wrong")
	}
	if err := interp.RunSource("rename n id\n"); err != nil {
		t.Fatal(err)
	}
	if _, ok := interp.table[0]["id"]; !ok {
		t.Error("rename did not add new field")
	}
	if _, ok := interp.table[0]["n"]; ok {
		t.Error("rename did not remove old field")
	}
	if err := interp.RunSource("drop tag\n"); err != nil {
		t.Fatal(err)
	}
	if _, ok := interp.table[0]["tag"]; ok {
		t.Error("drop did not remove field")
	}
	if err := interp.RunSource("clear\n"); err != nil {
		t.Fatal(err)
	}
	if interp.RowCount() != 0 {
		t.Errorf("clear left %d rows", interp.RowCount())
	}
	if c := interp.vars["count"]; c.i != 0 {
		t.Errorf("count after clear = %d, want 0", c.i)
	}
}

func TestEchoAndKey(t *testing.T) {
	interp, buf := runSrc(t, "echo hello \"big world\"\nset k 1\nkey hunter2\n")
	if !strings.Contains(buf.String(), "hello big world") {
		t.Errorf("echo output = %q", buf.String())
	}
	if interp.Key() != "hunter2" {
		t.Errorf("key = %q, want hunter2", interp.Key())
	}
}

func TestKeyedDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vault.dcdb")
	if err := SaveDoc(path, ObjValue(map[string]Value{
		"vault": ObjValue(map[string]Value{"pin": IntValue(1234)}),
	}), "mykey"); err != nil {
		t.Fatal(err)
	}
	interp := New(&bytes.Buffer{}, false)
	interp.SetBaseDir(dir)
	interp.SetKey("mykey")
	if err := interp.RunSource("load " + path + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := interp.RunSource("use vault\n"); err != nil {
		t.Fatal(err)
	}
	if !ValueEqual(interp.table[0]["value"], IntValue(1234)) {
		t.Error("keyed load wrong value")
	}
	wrong := New(&bytes.Buffer{}, false)
	wrong.SetBaseDir(dir)
	wrong.SetKey("badkey")
	err := wrong.RunSource("load " + path + "\n")
	if err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Errorf("expected corrupt error with wrong key, got %v", err)
	}
}

func TestMath(t *testing.T) {
	interp, _ := runSrc(t, "set x 5\n++ x\n-- x\n+ x 10\n- x 3\n* x 2\n/ x 4\n")
	if v := interp.vars["x"]; v.i != 6 {
		t.Fatalf("x = %d, want 6", v.i)
	}
	if err := interp.RunSource("/ x 0\n"); err != nil {
		t.Fatal(err)
	}
	if v := interp.vars["x"]; v.i != 6 {
		t.Fatalf("x after div by zero = %d, want unchanged 6", v.i)
	}
}

func TestPrintOutput(t *testing.T) {
	_, buf := runSrc(t, "add name=\"Alice\" age=34\nprint\n")
	out := buf.String()
	if !strings.Contains(out, "\"Alice\"") || !strings.Contains(out, "34") {
		t.Errorf("print output missing data: %q", out)
	}
}

func TestParserErrors(t *testing.T) {
	cases := map[string]string{
		"loda users\n":                "unknown command",
		"load \"abc\n":                "unterminated quoted string",
		"while x > 0\n":               "missing end",
		"end\n":                       "unexpected end",
		"only a ==\n":                 "",
		"add x 5\n":                   "invalid field=value",
		"sort\n":                      "sort requires",
		"while\n":                     "missing end",
		"use a.b\n":                   "path not found",
		"if wow\n":                    "missing end",
		"else swing\n":                "else without if",
		"if x > 0\nelse\nelse\nend\n": "duplicate else",
		"run wow\n":                   "unknown command: run",
		"++ missing\n":                "requires a numeric variable",
		"+ x 1\n":                     "requires a numeric variable",
		"for i 0 0 0\n++ i\nend\n":    "step cannot be zero",
	}
	for src, want := range cases {
		interp := New(&bytes.Buffer{}, false)
		interp.SetBaseDir(t.TempDir())
		err := interp.RunSource(src)
		if err == nil {
			t.Errorf("RunSource(%q) expected error", src)
			continue
		}
		if want != "" && !strings.Contains(err.Error(), want) {
			t.Errorf("RunSource(%q) error = %q, want contains %q", src, err.Error(), want)
		}
	}
}

func TestPreflightBeforeExecute(t *testing.T) {
	interp := New(&bytes.Buffer{}, false)
	interp.SetBaseDir(t.TempDir())
	err := interp.RunSource("add n=1\nloda users\nadd n=2\n")
	if err == nil {
		t.Fatal("expected parser error")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error should point at line 2: %v", err)
	}
	if interp.RowCount() != 0 {
		t.Errorf("table mutated before preflight, rows = %d", interp.RowCount())
	}
}

func TestBigRoundTripParallel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.dcdb")
	rows := make([]Row, 0, 2048)
	for i := 0; i < 2048; i++ {
		rows = append(rows, Row{"id": IntValue(int64(i)),
			"name": StrValue("user-" + strings.Repeat("x", i%50))})
	}
	if err := SaveDoc(path, makeDoc(rows), "test"); err != nil {
		t.Fatal(err)
	}
	doc, err := LoadDoc(path, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.a) != 2048 {
		t.Fatalf("loaded %d rows, want 2048", len(doc.a))
	}
	if !ValueEqual(doc.a[2047].o["id"], IntValue(2047)) {
		t.Error("last row wrong after parallel roundtrip")
	}
}

func TestParallelRowOps(t *testing.T) {
	interp := New(&bytes.Buffer{}, false)
	interp.SetBaseDir(t.TempDir())
	rows := make([]Row, 0, 2000)
	for i := 0; i < 2000; i++ {
		rows = append(rows, Row{"id": IntValue(int64(i)), "tag": StrValue("t")})
	}
	interp.table = rows
	if err := interp.RunSource("only id < 1000\n"); err != nil {
		t.Fatal(err)
	}
	if n := interp.RowCount(); n != 1000 {
		t.Fatalf("rows after only = %d, want 1000", n)
	}
	if !ValueEqual(interp.table[0]["id"], IntValue(0)) {
		t.Error("order lost in parallel filter")
	}
	if !ValueEqual(interp.table[999]["id"], IntValue(999)) {
		t.Error("tail order lost in parallel filter")
	}
	if err := interp.RunSource("update note hi id 5\n"); err != nil {
		t.Fatal(err)
	}
	if !ValueEqual(interp.table[5]["note"], StrValue("hi")) {
		t.Error("parallel update missed row")
	}
	if err := interp.RunSource("show id\n"); err != nil {
		t.Fatal(err)
	}
	if _, ok := interp.table[0]["tag"]; ok {
		t.Error("show did not project fields")
	}
	if !ValueEqual(interp.table[0]["id"], IntValue(0)) {
		t.Error("show lost order")
	}
}

func TestSortAndTake(t *testing.T) {
	interp, _ := runSrc(t, "add v=30\nadd v=10\nadd v=20\nsort v asc\n")
	if !ValueEqual(interp.table[0]["v"], IntValue(10)) {
		t.Error("asc sort wrong at index 0")
	}
	if !ValueEqual(interp.table[2]["v"], IntValue(30)) {
		t.Error("asc sort wrong at index 2")
	}
	if err := interp.RunSource("sort v\n"); err != nil {
		t.Fatal(err)
	}
	if !ValueEqual(interp.table[0]["v"], IntValue(30)) {
		t.Error("des sort wrong at index 0")
	}
	if err := interp.RunSource("take 1\n"); err != nil {
		t.Fatal(err)
	}
	if interp.RowCount() != 1 {
		t.Errorf("take kept %d rows, want 1", interp.RowCount())
	}
}

func TestExitStopsRun(t *testing.T) {
	interp := New(&bytes.Buffer{}, false)
	interp.SetBaseDir(t.TempDir())
	err := interp.RunSource("add n=1\nexit\nadd n=2\n")
	if err != errExit {
		t.Fatalf("err = %v, want errExit", err)
	}
	if interp.RowCount() != 1 {
		t.Errorf("rows after exit = %d, want 1", interp.RowCount())
	}
}

func TestUpdateMatching(t *testing.T) {
	interp, _ := runSrc(t, "add name=\"Alice\" age=34\nadd name=\"Bob\" age=17\nupdate age 99 name Bob\n")
	if !ValueEqual(interp.table[0]["age"], IntValue(34)) {
		t.Error("update changed wrong row")
	}
	if !ValueEqual(interp.table[1]["age"], IntValue(99)) {
		t.Error("update missed target row")
	}
}

func TestJoin(t *testing.T) {
	dir := t.TempDir()
	usersPath := filepath.Join(dir, "users.dcdb")
	ordersPath := filepath.Join(dir, "orders.dcdb")
	users := ObjValue(map[string]Value{
		"7": ObjValue(map[string]Value{"name": StrValue("Alice")}),
		"9": ObjValue(map[string]Value{"name": StrValue("Bob")}),
	})
	if err := SaveDoc(usersPath, users, ""); err != nil {
		t.Fatal(err)
	}
	interp := New(&bytes.Buffer{}, false)
	interp.SetBaseDir(dir)
	build := "add user_id=7 item=\"shirt\"\nadd user_id=9 item=\"cup\"\nadd user_id=42 item=\"lamp\"\nsave " + ordersPath + "\n"
	if err := interp.RunSource(build); err != nil {
		t.Fatal(err)
	}
	if err := interp.RunSource("load " + ordersPath + "\njoin " + usersPath + " on user_id _id\n"); err != nil {
		t.Fatal(err)
	}
	if n := interp.RowCount(); n != 2 {
		t.Fatalf("inner join rows = %d, want 2", n)
	}
	if !ValueEqual(interp.table[0]["name"], StrValue("Alice")) {
		t.Errorf("row0 name = %v, want Alice", interp.table[0]["name"])
	}
	if !ValueEqual(interp.table[1]["name"], StrValue("Bob")) {
		t.Errorf("row1 name = %v, want Bob", interp.table[1]["name"])
	}
	if err := interp.RunSource("load " + ordersPath + "\njoin left " + usersPath + " on user_id _id\n"); err != nil {
		t.Fatal(err)
	}
	if n := interp.RowCount(); n != 3 {
		t.Fatalf("left join rows = %d, want 3", n)
	}
	found := map[string]bool{}
	for _, r := range interp.table {
		found[stringify(r["item"])] = true
	}
	if !found["lamp"] {
		t.Error("left join dropped unmatched row")
	}
}

func TestIndexFastPath(t *testing.T) {
	interp, _ := runSrc(t, "add tag=\"a\" n=1\nadd tag=\"b\" n=2\nadd tag=\"a\" n=3\nindex tag\nonly tag == \"a\"\n")
	if n := interp.RowCount(); n != 2 {
		t.Fatalf("indexed only rows = %d, want 2", n)
	}
	if !ValueEqual(interp.table[0]["n"], IntValue(1)) {
		t.Error("indexed only lost order")
	}
	if err := interp.RunSource("clear\nadd tag=\"x\" n=5\nindex tag\nadd tag=\"y\" n=6\nupdate n 99 tag \"y\"\n"); err != nil {
		t.Fatal(err)
	}
	if !ValueEqual(interp.table[1]["n"], IntValue(99)) {
		t.Error("indexed update missed")
	}
	if err := interp.RunSource("only tag == \"y\"\n"); err != nil {
		t.Fatal(err)
	}
	if n := interp.RowCount(); n != 1 {
		t.Fatalf("filter after indexed update = %d, want 1", n)
	}
	if !ValueEqual(interp.table[0]["n"], IntValue(99)) {
		t.Error("updated row lookup wrong")
	}
	if err := interp.RunSource("clear\nadd n=1\nindex n\nindex drop n\nonly n == 1\n"); err != nil {
		t.Fatal(err)
	}
	if interp.RowCount() != 1 {
		t.Errorf("after index drop, filter failed")
	}
}

func TestTransactions(t *testing.T) {
	interp, _ := runSrc(t, "add n=1\nbegin\nadd n=2\nrollback\n")
	if n := interp.RowCount(); n != 1 {
		t.Fatalf("rollback left %d rows, want 1", n)
	}
	if err := interp.RunSource("begin\nadd n=2\ncommit\n"); err != nil {
		t.Fatal(err)
	}
	if n := interp.RowCount(); n != 2 {
		t.Fatalf("commit left %d rows, want 2", n)
	}
	if err := interp.RunSource("begin\nadd n=3\nloda bad\n"); err == nil {
		t.Fatal("expected error in transaction")
	}
	if n := interp.RowCount(); n != 2 {
		t.Fatalf("auto rollback left %d rows, want 2", n)
	}
	if err := interp.RunSource("set v 1\nbegin\nset v 2\nrollback\n"); err != nil {
		t.Fatal(err)
	}
	if v := interp.vars["v"]; v.i != 1 {
		t.Errorf("rollback did not restore variable, v = %v", v)
	}
	if err := interp.RunSource("commit\n"); err == nil {
		t.Error("commit without begin should error")
	}
}

func TestProcCall(t *testing.T) {
	interp, _ := runSrc(t, "set total 0\nproc bump\n+ total 1\nend\ncall bump\ncall bump\n")
	if v := interp.vars["total"]; v.i != 2 {
		t.Fatalf("total after calls = %d, want 2", v.i)
	}
	if err := interp.RunSource("call nope\n"); err == nil {
		t.Error("call to missing proc should error")
	}
	if err := interp.RunSource("proc nope\n+ total 1\n"); err == nil {
		t.Error("unterminated proc should error")
	}
}

func TestInclude(t *testing.T) {
	dir := t.TempDir()
	lib := filepath.Join(dir, "lib.dc")
	if err := os.WriteFile(lib, []byte("set lib 7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	interp := New(&bytes.Buffer{}, false)
	interp.SetBaseDir(dir)
	if err := interp.RunSource("include lib\n"); err != nil {
		t.Fatal(err)
	}
	if v := interp.vars["lib"]; v.i != 7 {
		t.Errorf("lib = %v, want 7", v)
	}
	if err := interp.RunSource("include missing\n"); err == nil {
		t.Error("include of missing file should error")
	}
}

func TestHashCommand(t *testing.T) {
	_, buf := runSrc(t, "hash abc\n")
	out := strings.TrimSpace(buf.String())
	want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if out != want {
		t.Errorf("hash abc = %q, want %q", out, want)
	}
}

func TestOrderingError(t *testing.T) {
	interp := New(&bytes.Buffer{}, false)
	interp.SetBaseDir(t.TempDir())
	err := interp.RunSource("add name=\"x\"\nonly name > \"a\"\n")
	if err == nil || !strings.Contains(err.Error(), "cannot order") {
		t.Errorf("expected cannot order error, got %v", err)
	}
	if err := interp.RunSource("only name == \"x\"\n"); err != nil {
		t.Fatalf("equality filter should still work: %v", err)
	}
	if err := interp.RunSource("set s \"a\"\nwhile s > \"b\"\nend\n"); err == nil {
		t.Error("while with string ordering should error")
	}
}

func TestPreflightReportsAll(t *testing.T) {
	interp := New(&bytes.Buffer{}, false)
	interp.SetBaseDir(t.TempDir())
	err := interp.RunSource("loda a\nset x 1\nspin b\nadd y=2\n")
	if err == nil {
		t.Fatal("expected parser errors")
	}
	msg := err.Error()
	if !strings.Contains(msg, "line 1") || !strings.Contains(msg, "line 3") {
		t.Errorf("expected all errors reported, got %q", msg)
	}
	if interp.RowCount() != 0 {
		t.Errorf("table mutated despite parse errors, rows = %d", interp.RowCount())
	}
}

func TestLegacyV1Loads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old.dcdb")
	payload := encodeValuePayload(ObjValue(map[string]Value{"a": IntValue(1)}))
	data := append([]byte(dbMagic), 1)
	data = append(data, payload...)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := LoadDoc(path, "anything")
	if err != nil {
		t.Fatal(err)
	}
	if !ValueEqual(doc.o["a"], IntValue(1)) {
		t.Error("v1 plaintext load wrong")
	}
}

func TestLegacyV2Loads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old2.dcdb")
	payload := encodeValuePayload(ObjValue(map[string]Value{"a": IntValue(2)}))
	iv := make([]byte, 16)
	if _, err := rand.Read(iv); err != nil {
		t.Fatal(err)
	}
	ct := cryptCTR(payload, deriveKeyV2("legacy"), iv)
	var buf bytes.Buffer
	buf.WriteString(dbMagic)
	buf.WriteByte(2)
	buf.Write(iv)
	var cr [4]byte
	binary.LittleEndian.PutUint32(cr[:], crc32.ChecksumIEEE(payload))
	buf.Write(cr[:])
	buf.Write(ct)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDoc(path, "wrong"); err == nil {
		t.Error("v2 wrong key should fail")
	}
	doc, err := LoadDoc(path, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if !ValueEqual(doc.o["a"], IntValue(2)) {
		t.Error("v2 load wrong")
	}
}

func TestV3WrongKeyAndNoLeak(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v3.dcdb")
	if err := SaveDoc(path, ObjValue(map[string]Value{"secret": StrValue("hunter2")}), "right"); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDoc(path, "wrong"); err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Errorf("wrong key = %v, want corrupt", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("hunter2")) || bytes.Contains(raw, []byte("secret")) {
		t.Error("plaintext leaked into v3 file")
	}
	if len(raw) < 5+keySaltLen+gcmIVLen+16 {
		t.Error("v3 header too small")
	}
}
