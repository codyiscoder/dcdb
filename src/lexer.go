package main

import (
	"fmt"
	"strings"
)

type Line struct {
	cmd  string
	args []string
}

type ParserError struct {
	Line int
	Msg  string
}

func (this *ParserError) Error() string {
	if this.Line > 0 {
		return fmt.Sprintf("Parser Error: line %d: %s", this.Line, this.Msg)
	}
	return fmt.Sprintf("Parser Error: %s", this.Msg)
}

func parserError(line int, format string, args ...interface{}) error {
	return &ParserError{Line: line, Msg: fmt.Sprintf(format, args...)}
}

func knownCommand(cmd string) bool {
	switch cmd {
	case "load", "use", "only", "delete", "sort", "take", "add", "update",
		"save", "show", "print", "printvar", "set", "while", "for", "if",
		"else", "end", "exit", "sleep", "++", "--", "+", "-", "*", "/",
		"clear", "drop", "rename", "unique", "reverse", "echo", "key", "run",
		"join", "index", "begin", "commit", "rollback", "proc", "call",
		"include", "hash":
		return true
	}
	return false
}

func (this Line) blockDelta() int {
	switch this.cmd {
	case "while", "for", "if", "proc":
		return 1
	case "end":
		return -1
	}
	return 0
}

type blockFrame struct {
	line    int
	kind    string
	sawElse bool
}

func splitArgs(line string) ([]string, error) {
	var args []string
	var tok strings.Builder
	inTok := false
	inQuote := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if inQuote {
			if c == '"' {
				inQuote = false
				continue
			}
			tok.WriteByte(c)
			continue
		}
		if c == '"' {
			inQuote = true
			inTok = true
			continue
		}
		if c == '/' && i+1 < len(line) && line[i+1] == '/' {
			break
		}
		if c == ' ' || c == '\t' {
			if inTok {
				args = append(args, tok.String())
				tok.Reset()
				inTok = false
			}
			continue
		}
		tok.WriteByte(c)
		inTok = true
	}
	if inQuote {
		return nil, fmt.Errorf("unterminated quoted string")
	}
	if inTok {
		args = append(args, tok.String())
	}
	return args, nil
}

func Parse(src string) ([]Line, error) {
	raw := strings.Split(src, "\n")
	lines := make([]Line, 0, len(raw))
	var stack []blockFrame
	for i := 0; i < len(raw); i++ {
		trimmed := strings.TrimRight(raw[i], "\r")
		args, err := splitArgs(trimmed)
		if err != nil {
			return nil, parserError(i+1, "%s", err.Error())
		}
		if len(args) == 0 {
			continue
		}
		cmd := strings.ToLower(args[0])
		if !knownCommand(cmd) {
			return nil, parserError(i+1, "unknown command %q", args[0])
		}
		ln := Line{cmd: cmd, args: args[1:]}
		lines = append(lines, ln)
		switch ln.blockDelta() {
		case 1:
			stack = append(stack, blockFrame{line: i + 1, kind: cmd})
		case -1:
			if len(stack) == 0 {
				return nil, parserError(i+1, "unexpected end")
			}
			stack = stack[:len(stack)-1]
		}
		if cmd == "else" {
			if len(stack) == 0 {
				return nil, parserError(i+1, "else without if")
			}
			top := &stack[len(stack)-1]
			if top.kind != "if" {
				return nil, parserError(i+1, "else outside if block")
			}
			if top.sawElse {
				return nil, parserError(i+1, "duplicate else")
			}
			top.sawElse = true
		}
	}
	if len(stack) > 0 {
		f := stack[len(stack)-1]
		return nil, parserError(f.line, "missing end for block")
	}
	return lines, nil
}

func Validate(src string) []error {
	var errs []error
	raw := strings.Split(src, "\n")
	var stack []blockFrame
	for i := 0; i < len(raw); i++ {
		trimmed := strings.TrimRight(raw[i], "\r")
		args, err := splitArgs(trimmed)
		if err != nil {
			errs = append(errs, parserError(i+1, "%s", err.Error()))
			continue
		}
		if len(args) == 0 {
			continue
		}
		cmd := strings.ToLower(args[0])
		if !knownCommand(cmd) {
			errs = append(errs, parserError(i+1, "unknown command %q", args[0]))
			continue
		}
		ln := Line{cmd: cmd, args: args[1:]}
		switch ln.blockDelta() {
		case 1:
			stack = append(stack, blockFrame{line: i + 1, kind: cmd})
		case -1:
			if len(stack) == 0 {
				errs = append(errs, parserError(i+1, "unexpected end"))
			} else {
				stack = stack[:len(stack)-1]
			}
		}
		if cmd == "else" {
			if len(stack) == 0 {
				errs = append(errs, parserError(i+1, "else without if"))
				continue
			}
			top := &stack[len(stack)-1]
			if top.kind != "if" {
				errs = append(errs, parserError(i+1, "else outside if block"))
				continue
			}
			if top.sawElse {
				errs = append(errs, parserError(i+1, "duplicate else"))
				continue
			}
			top.sawElse = true
		}
	}
	if len(stack) > 0 {
		f := stack[len(stack)-1]
		errs = append(errs, parserError(f.line, "missing end for block"))
	}
	return errs
}

func DepthOf(line string) (int, error) {
	args, err := splitArgs(line)
	if err != nil {
		return 0, parserError(0, "%s", err.Error())
	}
	if len(args) == 0 {
		return 0, nil
	}
	ln := Line{cmd: strings.ToLower(args[0]), args: args[1:]}
	return ln.blockDelta(), nil
}
