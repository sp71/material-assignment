// Command memfsh is an interactive shell over the in-memory filesystem in
// package memfs. It exists to exercise the library by hand and to demo the
// available operations; nothing is persisted, so the tree is lost on exit.
//
// Run it with:
//
//	go run ./cmd/memfsh
//
// then type "help" for the command list.
package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"

	"memfs"
)

func main() {
	fs := memfs.New()
	in := bufio.NewScanner(os.Stdin)

	fmt.Println("in-memory filesystem shell — type 'help' for commands, 'exit' to quit")
	for {
		fmt.Printf("%s$ ", fs.Pwd())
		if !in.Scan() {
			fmt.Println() // newline after EOF (Ctrl-D)
			break
		}
		fields := strings.Fields(in.Text())
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "exit" || fields[0] == "quit" {
			break
		}
		if err := dispatch(fs, fields); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
	}
	if err := in.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "read error:", err)
		os.Exit(1)
	}
}

// command describes a single shell verb. run receives the filesystem and the
// argument tokens after the verb itself.
type command struct {
	args string // argument summary for `help`
	help string
	min  int // minimum number of arguments required
	run  func(fs *memfs.FS, args []string) error
}

// commands is the dispatch table. Keeping verbs in data (rather than a giant
// switch) makes `help` self-describing and adding a verb a one-line change.
var commands = map[string]command{
	"pwd":    {help: "print the working directory", run: cmdPwd},
	"ls":     {help: "list the working directory's children", run: cmdLs},
	"cd":     {args: "<dir>", help: "change directory (use .. for parent)", min: 1, run: cmdCd},
	"mkdir":  {args: "<name>", help: "create a directory", min: 1, run: cmdMkdir},
	"rmdir":  {args: "<name>", help: "remove an empty directory", min: 1, run: cmdRmdir},
	"rmall":  {args: "<name>", help: "remove a directory and its subtree", min: 1, run: cmdRmall},
	"touch":  {args: "<name>", help: "create an empty file", min: 1, run: cmdTouch},
	"write":  {args: "<name> <text...>", help: "replace a file's contents", min: 1, run: cmdWrite},
	"cat":    {args: "<name>", help: "print a file's contents", min: 1, run: cmdCat},
	"rm":     {args: "<name>", help: "remove a file", min: 1, run: cmdRm},
	"mv":     {args: "<old> <new>", help: "rename a file in place", min: 2, run: cmdMv},
	"find":   {args: "<name>", help: "find descendants with an exact name", min: 1, run: cmdFind},
	"findre": {args: "<pattern>", help: "find descendants matching a regexp", min: 1, run: cmdFindRegex},
	"tree":   {help: "print the working directory's subtree", run: cmdTree},
}

func dispatch(fs *memfs.FS, fields []string) error {
	verb, args := fields[0], fields[1:]
	if verb == "help" {
		printHelp()
		return nil
	}
	cmd, ok := commands[verb]
	if !ok {
		return fmt.Errorf("unknown command %q (try 'help')", verb)
	}
	if len(args) < cmd.min {
		return fmt.Errorf("usage: %s %s", verb, cmd.args)
	}
	return cmd.run(fs, args)
}

func printHelp() {
	verbs := make([]string, 0, len(commands)+1)
	for v := range commands {
		verbs = append(verbs, v)
	}
	verbs = append(verbs, "help")
	sort.Strings(verbs)

	fmt.Println("commands:")
	for _, v := range verbs {
		if v == "help" {
			fmt.Println("  help                 show this message")
			continue
		}
		c := commands[v]
		fmt.Printf("  %-20s %s\n", strings.TrimSpace(v+" "+c.args), c.help)
	}
	fmt.Println("  exit | quit          leave the shell")
}

// --- command implementations ---

func cmdPwd(fs *memfs.FS, _ []string) error {
	fmt.Println(fs.Pwd())
	return nil
}

func cmdLs(fs *memfs.FS, _ []string) error {
	if names := fs.Ls(); len(names) > 0 {
		fmt.Println(strings.Join(names, "  "))
	}
	return nil
}

func cmdCd(fs *memfs.FS, args []string) error    { return fs.Cd(args[0]) }
func cmdMkdir(fs *memfs.FS, args []string) error { return fs.Mkdir(args[0]) }
func cmdRmdir(fs *memfs.FS, args []string) error { return fs.RemoveDir(args[0]) }
func cmdRmall(fs *memfs.FS, args []string) error { return fs.RemoveAll(args[0]) }
func cmdTouch(fs *memfs.FS, args []string) error { return fs.CreateFile(args[0]) }
func cmdRm(fs *memfs.FS, args []string) error    { return fs.Remove(args[0]) }
func cmdMv(fs *memfs.FS, args []string) error    { return fs.Move(args[0], args[1]) }

// cmdWrite joins the remaining tokens as the file's contents, so
// `write notes hello there` stores "hello there".
func cmdWrite(fs *memfs.FS, args []string) error {
	return fs.WriteFile(args[0], []byte(strings.Join(args[1:], " ")))
}

func cmdCat(fs *memfs.FS, args []string) error {
	data, err := fs.ReadFile(args[0])
	if err != nil {
		return err
	}
	os.Stdout.Write(data)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		fmt.Println()
	}
	return nil
}

func cmdFind(fs *memfs.FS, args []string) error {
	for _, p := range fs.Find(args[0]) {
		fmt.Println(p)
	}
	return nil
}

func cmdFindRegex(fs *memfs.FS, args []string) error {
	matches, err := fs.FindRegex(args[0])
	if err != nil {
		return err
	}
	for _, p := range matches {
		fmt.Println(p)
	}
	return nil
}

// cmdTree prints the subtree rooted at the working directory, indented by depth
// and relative to that directory's path.
func cmdTree(fs *memfs.FS, _ []string) error {
	base := fs.Pwd()
	return fs.Walk(func(path string, isDir bool) error {
		rel := strings.TrimPrefix(strings.TrimPrefix(path, base), "/")
		depth := strings.Count(rel, "/")
		name := rel[strings.LastIndex(rel, "/")+1:]
		marker := ""
		if isDir {
			marker = "/"
		}
		fmt.Printf("%s%s%s\n", strings.Repeat("  ", depth), name, marker)
		return nil
	})
}
