package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var version = "0.1.0"

func usage() {
	fmt.Fprintf(os.Stderr, "dcdb %s - a tiny data scripting language\n", version)
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "usage: dcdb [options] script.dc [script2.dc ...]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "options:")
	fmt.Fprintln(os.Stderr, "  -p, --print          enable print/printvar and dump the final table")
	fmt.Fprintln(os.Stderr, "  -t, --time           print total execution time")
	fmt.Fprintln(os.Stderr, "  -k, --key KEY        database encryption key")
	fmt.Fprintln(os.Stderr, "  -dp, --data-path P   create an empty database file at P if missing")
	fmt.Fprintln(os.Stderr, "  -op, --output-path P make sure the parent folder of P exists")
	fmt.Fprintln(os.Stderr, "  -v, --version        show version")
	fmt.Fprintln(os.Stderr, "  -h, --help           show this help")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "with no scripts, dcdb starts its built-in query console.")
}

func main() {
	printOn := false
	timeIt := false
	showVersion := false
	var dataPath string
	var outputPath string
	var keyFlag string

	flag.BoolVar(&printOn, "p", false, "enable print output")
	flag.BoolVar(&printOn, "print", false, "enable print output")
	flag.BoolVar(&timeIt, "t", false, "print total execution time")
	flag.BoolVar(&timeIt, "time", false, "print total execution time")
	flag.BoolVar(&showVersion, "v", false, "show version")
	flag.BoolVar(&showVersion, "version", false, "show version")
	flag.StringVar(&keyFlag, "k", "", "database encryption key")
	flag.StringVar(&keyFlag, "key", "", "database encryption key")
	flag.StringVar(&dataPath, "dp", "", "create empty database file")
	flag.StringVar(&dataPath, "data-path", "", "create empty database file")
	flag.StringVar(&outputPath, "op", "", "ensure output parent folder exists")
	flag.StringVar(&outputPath, "output-path", "", "ensure output parent folder exists")
	flag.Usage = usage
	flag.Parse()

	if showVersion {
		fmt.Printf("dcdb %s\n", version)
		return
	}

	scripts := flag.Args()
	for _, s := range scripts {
		if !strings.HasSuffix(s, ".dc") {
			fmt.Fprintln(os.Stderr, "Error: invalid script:", s)
			os.Exit(1)
		}
	}

	interp := New(os.Stdout, printOn)
	interp.SetKey(keyFlag)

	if dataPath != "" {
		if _, err := os.Stat(dataPath); os.IsNotExist(err) {
			if err := SaveDoc(dataPath, ArrValue(nil), keyFlag); err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err.Error())
				os.Exit(1)
			}
		}
	}
	if outputPath != "" {
		if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err.Error())
			os.Exit(1)
		}
	}

	if len(scripts) == 0 {
		repl(interp)
		return
	}

	start := time.Now()
	for _, s := range scripts {
		err := interp.RunFile(s)
		if err == errExit {
			break
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
	}
	if printOn {
		interp.PrintTable()
	}
	if timeIt {
		fmt.Fprintf(os.Stderr, "took %s\n", time.Since(start))
	}
}

func printHelp(out io.Writer) {
	fmt.Fprintln(out, "rows:")
	fmt.Fprintln(out, "  load <name>     use <path>     only <f> <op> <v>   delete <f> <op> <v>")
	fmt.Fprintln(out, "  sort <f> [asc]  take <n>       add <f>=<v> ...     update <f> <v> <wf> <wv>")
	fmt.Fprintln(out, "  save <name>     show [f...]    drop <f> ...        rename <old> <new>")
	fmt.Fprintln(out, "  unique <f>      reverse        clear               join <name> on <lf> <rf>")
	fmt.Fprintln(out, "  join ... left   index <f>      index drop")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "print & variables:")
	fmt.Fprintln(out, "  print    printvar <var>    set <var> <v>    echo <text>    hash <text>")
	fmt.Fprintln(out, "  while <var> <op> <v>   for <var> <a> <b> [step]   if <var> <op> <v>")
	fmt.Fprintln(out, "  else     end    proc <name>    call <name>    include <file>")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "transactions:")
	fmt.Fprintln(out, "  begin    commit    rollback")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "math & misc:")
	fmt.Fprintln(out, "  ++ <v>  -- <v>  + <v> <n>  - <v> <n>  * <v> <n>  / <v> <n>")
	fmt.Fprintln(out, "  key <pass>    sleep <ms>    exit")
	fmt.Fprintln(out, "operators: > >= < <= == !=")
	fmt.Fprintln(out, "console: .help  .exit   (Ctrl+D also exits)")
}

func repl(interp *Interpreter) {
	interp.SetPrint(true)
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Fprintf(interp.out, "dcdb %s - built-in query console\n", version)
	fmt.Fprintln(interp.out, "")
	printHelp(interp.out)
	fmt.Fprintln(interp.out, "")
	var buffer []string
	depth := 0
	prompt := "> "
	for {
		fmt.Fprint(interp.out, prompt)
		if !scanner.Scan() {
			fmt.Fprintln(interp.out)
			break
		}
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == ".exit" {
			break
		}
		if trimmed == ".help" {
			printHelp(interp.out)
			continue
		}
		if trimmed == "" {
			if depth == 0 {
				interp.PrintTable()
			}
			continue
		}
		buffer = append(buffer, line)
		delta, err := DepthOf(line)
		if err != nil {
			fmt.Fprintln(interp.out, err.Error())
			buffer = buffer[:len(buffer)-1]
			continue
		}
		depth += delta
		if depth > 0 {
			prompt = "... "
			continue
		}
		prompt = "> "
		src := strings.Join(buffer, "\n")
		buffer = buffer[:0]
		depth = 0
		err = interp.RunSource(src)
		if err == errExit {
			break
		}
		if err != nil {
			fmt.Fprintln(interp.out, err.Error())
			continue
		}
		if dataCommands[interp.LastCommand()] {
			interp.PrintTable()
		}
	}
}
