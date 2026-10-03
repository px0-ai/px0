package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Test harnesses are the fakes that stand in for a coding agent: something that
// appends to a file, prints a line, sleeps, or exits with a code.
//
// writeHarness used to write a .sh file with a #!/bin/sh header. That is fine on
// Unix and cannot work on Windows at all -- exec fails with "%1 is not a valid
// Win32 application" -- so every harness-backed test failed there without
// testing anything. A .cmd replacement is no better for the tests that assert on
// the prompt itself: cmd.exe parses the command line itself and treats the
// newlines inside a multi-line prompt as command separators, so the harness
// receives only the first chunk of it.
//
// So the fake is the test binary re-executing itself. The prompt arrives as a
// real argv entry, with no shell in between, exactly as a real harness would
// receive it, and the body travels in an environment variable because that is
// the one channel the caller (px0) does not build.
//
// The vocabulary is deliberately tiny. An unrecognised line is a hard failure,
// so a new form cannot quietly pass on one platform and do nothing on another.
const harnessBodyEnv = "PX0_TEST_HARNESS_BODY"

// TestMain lets the test binary act as a harness when it is re-executed as one.
// The check has to happen before m.Run, otherwise the child would run the whole
// suite.
func TestMain(m *testing.M) {
	if body, ok := os.LookupEnv(harnessBodyEnv); ok {
		os.Exit(runHarnessBody(body, os.Args[1:]))
	}
	os.Exit(m.Run())
}

// writeHarness returns a command template that runs the fake harness. The
// returned value goes through the same resolution as a real harness name, so
// this exercises the production path rather than bypassing it.
func writeHarness(t *testing.T, body string) string {
	t.Helper()
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		if err := validHarnessLine(strings.TrimSpace(line)); err != nil {
			t.Fatalf("writeHarness: %v", err)
		}
	}
	// The body is validated here and read from the environment by the child, so
	// the template itself carries only the prompt placeholder.
	t.Setenv(harnessBodyEnv, body)
	return testBinaryPath(t)
}

// writeNamedHarness is writeHarness for the tests that depend on the harness
// answering to a particular name, such as the one that checks claude's
// session-resume flags.
//
// The fake is the test binary, so on Windows it is copied to <name>.exe: Windows
// will not run an extensionless file, and a .cmd wrapper is not an option either
// because cmd.exe parses the command line itself and would split the
// multi-line prompt into separate commands. A copy of the binary keeps the
// prompt one real argument, exactly as a real harness receives it.
func writeNamedHarness(t *testing.T, name, body string) string {
	t.Helper()
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		if err := validHarnessLine(strings.TrimSpace(line)); err != nil {
			t.Fatalf("writeNamedHarness: %v", err)
		}
	}
	t.Setenv(harnessBodyEnv, body)

	dir := t.TempDir()
	dst := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		dst += ".exe"
	}
	if err := copyFile(testBinaryPath(t), dst); err != nil {
		t.Fatalf("writeNamedHarness: %v", err)
	}
	return dst
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o755)
}

func testBinaryPath(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locating the test binary: %v", err)
	}
	return exe
}

// echoHarness is a harness spec that just prints its prompt, for the tests that
// need a runnable harness but assert nothing about what it did to the tree.
//
// Several of those tests used the literal spec "echo {prompt}", on the
// assumption that echo is an executable everywhere. It is not: on Windows echo
// is a cmd.exe builtin, so LookPath fails and selecting the harness errors out
// with "echo is not installed" before any of the behaviour under test runs. The
// test binary is the one executable that is guaranteed to exist and to be
// runnable in exactly the way px0 runs a real harness.
//
// The returned name is what the manager reports for it, which is the binary's
// own base name.
func echoHarness(t *testing.T) (spec, name string) {
	t.Helper()
	t.Setenv(harnessBodyEnv, `printf '%s' "$1"`)
	exe := testBinaryPath(t)
	// The manager reports the bare name, without the executable extension.
	return exe + " {prompt}", harnessDisplayName(exe)
}

func validHarnessLine(line string) error {
	if line == "" || strings.HasPrefix(line, "#") {
		return nil
	}
	switch {
	case strings.HasPrefix(line, "sleep "):
		if _, err := strconv.ParseFloat(strings.TrimPrefix(line, "sleep "), 64); err != nil {
			return fmt.Errorf("bad sleep in %q", line)
		}
		return nil
	case strings.HasPrefix(line, "exit "):
		if _, err := strconv.Atoi(strings.TrimPrefix(line, "exit ")); err != nil {
			return fmt.Errorf("bad exit in %q", line)
		}
		return nil
	case strings.HasPrefix(line, "printf "), strings.HasPrefix(line, "echo "):
		return nil
	}
	return fmt.Errorf("%q is not in the portable harness vocabulary "+
		"(printf, echo, sleep, exit); add an implementation rather than letting it "+
		"pass on one platform and do nothing on another", line)
}

// runHarnessBody interprets the body and returns the process exit code.
func runHarnessBody(body string, argv []string) int {
	// "$1" is the first argument, "$*" is all of them joined by spaces. A
	// multi-line prompt is one argument, so "$1" is the whole prompt and not
	// just its first line -- which is the whole reason this fake is not a shell
	// script.
	for _, raw := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if code, done := harnessSleepExit(line); done {
			// exit stops the turn; sleep only pauses it and the body continues.
			if strings.HasPrefix(line, "exit ") {
				return code
			}
			continue
		}
		if code, handled := harnessAppend(line, argv); handled {
			if code != 0 {
				return code
			}
			continue
		}
		harnessPrint(line, argv)
	}
	return 0
}

// harnessAppend handles the "write something to a file" forms:
//
//	printf 'text' >> file        (append a line)
//	echo "text" >> file          (append a line)
//	printf '%s' "$1" > file      (the prompt alone, no trailing newline)
//
// The file is resolved against the harness's working directory, which is the
// workspace, so a bare relative name behaves as it would in the shell version.
func harnessAppend(line string, argv []string) (int, bool) {
	if !strings.HasPrefix(line, "printf ") && !strings.HasPrefix(line, "echo ") {
		return 0, false
	}
	rest := line[strings.Index(line, " ")+1:]
	format, remainder, ok := splitQuoted(rest)
	if !ok {
		return 0, false
	}
	rest = strings.TrimSpace(remainder)

	var path, text string
	switch {
	case strings.HasPrefix(rest, ">>"):
		path = strings.TrimSpace(strings.TrimPrefix(rest, ">>"))
		arg, referenced := argRef(remainder, argv)
		format = unescapeNL(format)
		if referenced {
			// A body that appends the argument adds its own newline, which is
			// what the "\n" in the format was already asking for.
			format = strings.TrimSuffix(format, `\n`)
			text = expandArg(format, arg)
		} else {
			text = format
		}
		text += "\n"
	case strings.Contains(rest, ">"):
		// printf '%s' "$1" > file -- the format is only a placeholder, so the
		// argument is written and no newline is added.
		path = strings.TrimSpace(rest[strings.LastIndex(rest, ">")+1:])
		if !strings.Contains(format, "%s") {
			return 0, false
		}
		arg, _ := argRef(remainder, argv)
		text = expandArg(unescapeNL(format), arg)
	default:
		return 0, false
	}
	if path == "" {
		return 0, false
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "harness: %v\n", err)
		return 1, true
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		fmt.Fprintf(os.Stderr, "harness: %v\n", err)
		return 1, true
	}
	return 0, true
}

// harnessSleepExit handles the two forms that end the turn early.
func harnessSleepExit(line string) (int, bool) {
	if rest, ok := strings.CutPrefix(line, "sleep "); ok {
		secs, err := strconv.ParseFloat(strings.TrimSpace(rest), 64)
		if err != nil {
			return 1, true
		}
		time.Sleep(time.Duration(secs * float64(time.Second)))
		return 0, true
	}
	if rest, ok := strings.CutPrefix(line, "exit "); ok {
		code, err := strconv.Atoi(strings.TrimSpace(rest))
		if err != nil {
			return 1, true
		}
		return code, true
	}
	return 0, false
}

// harnessPrint handles the forms that only produce output:
//
//	printf 'text'
//	printf 'text %s more' "$1"
func harnessPrint(line string, argv []string) {
	rest := line[strings.Index(line, " ")+1:]
	format, remainder, ok := splitQuoted(rest)
	if !ok {
		return
	}
	// The body's own trailing \n is the line terminator, and Println supplies
	// one, so it is trimmed to avoid emitting a blank line between events.
	format = strings.TrimSuffix(unescapeNL(format), "\n")
	arg, referenced := argRef(remainder, argv)
	if referenced {
		format = expandArg(format, arg)
	}
	fmt.Println(format)
}

// splitQuoted pulls a single- or double-quoted token off the front of s.
func splitQuoted(s string) (token, rest string, ok bool) {
	if s == "" {
		return "", "", false
	}
	quote := s[0]
	if quote != '\'' && quote != '"' {
		return "", "", false
	}
	end := strings.IndexByte(s[1:], quote)
	if end < 0 {
		return "", "", false
	}
	return s[1 : 1+end], s[2+end:], true
}

// expandArg substitutes the argument a body refers to back into its format.
//
// The format arrives with the %s placeholders still in it, and which argument
// was meant is carried separately: "$1" means the first argument, "$*" means all
// of them joined by spaces. A format with no placeholder is a literal, which is
// why the "contains %s" test comes first.
func expandArg(format, value string) string {
	if !strings.Contains(format, "%s") {
		return format
	}
	return strings.ReplaceAll(format, "%s", value)
}

func unescapeNL(s string) string {
	return strings.NewReplacer(`\n`, "\n", `\t`, "\t", `\r`, "\r").Replace(s)
}

// argRef reports which argument a line's remainder refers to, and its value.
func argRef(remainder string, argv []string) (value string, referenced bool) {
	switch {
	case strings.Contains(remainder, `"$*"`):
		return strings.Join(argv, " "), true
	case strings.Contains(remainder, `"$1"`):
		if len(argv) > 0 {
			return argv[0], true
		}
		return "", true
	}
	return "", false
}
