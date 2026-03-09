package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// Writer provides TTY-aware formatted output for CLI commands.
type Writer struct {
	w     io.Writer
	color bool
}

// New creates a Writer that auto-detects TTY and respects NO_COLOR.
func New(w io.Writer) *Writer {
	return &Writer{w: w, color: isTTY(w) && os.Getenv("NO_COLOR") == ""}
}

func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// Symbol methods return TTY-aware glyphs.

func (o *Writer) OK() string {
	if o.color {
		return "✓"
	}
	return "+"
}

func (o *Writer) Fail() string {
	if o.color {
		return "✗"
	}
	return "x"
}

func (o *Writer) Warn() string {
	if o.color {
		return "⚠"
	}
	return "!"
}

func (o *Writer) Skip() string {
	if o.color {
		return "⏭"
	}
	return "-"
}

func (o *Writer) Arrow() string {
	if o.color {
		return "→"
	}
	return ">"
}

func (o *Writer) Dot() string {
	if o.color {
		return "●"
	}
	return "*"
}

// Color methods wrap text in ANSI codes when color is enabled.

func (o *Writer) Green(s string) string {
	if !o.color {
		return s
	}
	return "\033[32m" + s + "\033[0m"
}

func (o *Writer) Yellow(s string) string {
	if !o.color {
		return s
	}
	return "\033[33m" + s + "\033[0m"
}

func (o *Writer) Red(s string) string {
	if !o.color {
		return s
	}
	return "\033[31m" + s + "\033[0m"
}

func (o *Writer) Bold(s string) string {
	if !o.color {
		return s
	}
	return "\033[1m" + s + "\033[0m"
}

func (o *Writer) Dim(s string) string {
	if !o.color {
		return s
	}
	return "\033[2m" + s + "\033[0m"
}

// Output helpers

// Blank prints an empty line.
func (o *Writer) Blank() {
	fmt.Fprintln(o.w)
}

// Header prints a bold line.
func (o *Writer) Header(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(o.w, "  %s\n", o.Bold(msg))
}

// Line prints a symbol-prefixed indented line.
func (o *Writer) Line(symbol, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(o.w, "  %s %s\n", symbol, msg)
}

// ActionLine prints a formatted action line: symbol + padded operation + name + annotation.
func (o *Writer) ActionLine(symbol, operation, name, annotation string) {
	line := fmt.Sprintf("  %s %-8s %s", symbol, operation, name)
	if annotation != "" {
		line += "  " + annotation
	}
	fmt.Fprintln(o.w, line)
}

// Summary prints an indented summary line.
func (o *Writer) Summary(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(o.w, "  %s\n", msg)
}

// Next prints a "Next:" block with hint lines.
func (o *Writer) Next(lines ...string) {
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(o.w, "  %s %s\n", o.Bold("Next:"), lines[0])
	for _, l := range lines[1:] {
		fmt.Fprintf(o.w, "        %s\n", l)
	}
}

// ColorEnabled reports whether color output is active.
func (o *Writer) ColorEnabled() bool {
	return o.color
}

// NumberedAction prints a numbered action line for plan output.
func (o *Writer) NumberedAction(num int, operation, name, annotation string) {
	line := fmt.Sprintf("  %d. %-8s %s", num, operation, name)
	if annotation != "" {
		line += "  " + annotation
	}
	fmt.Fprintln(o.w, line)
}

// StatusLine prints a resource status line for status/drift output.
func (o *Writer) StatusLine(name, status, detail string) {
	line := fmt.Sprintf("  %-20s %-12s %s", name, status, detail)
	fmt.Fprintln(o.w, strings.TrimRight(line, " "))
}

// WriteJSON marshals v as indented JSON to w. Used by --format json.
func WriteJSON(w io.Writer, v interface{}) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// DiffLine prints a diff-style line: colored +/-/~ prefix with key and value.
func (o *Writer) DiffLine(operation, key, value string) {
	var prefix string
	switch operation {
	case "CREATE":
		prefix = o.Green("+")
	case "DELETE":
		prefix = o.Red("-")
	case "UPDATE":
		prefix = o.Yellow("~")
	default:
		prefix = " "
	}
	fmt.Fprintf(o.w, "      %s %s: %s\n", prefix, key, value)
}

// Confirm prompts the user for a yes/no response. Returns true only if the
// user types "y" or "yes". Returns false for non-TTY writers.
func (o *Writer) Confirm(prompt string) bool {
	if !isTTY(o.w) {
		return false
	}
	fmt.Fprintf(o.w, "  %s ", prompt)
	var response string
	if _, err := fmt.Scanln(&response); err != nil {
		return false
	}
	response = strings.ToLower(strings.TrimSpace(response))
	return response == "y" || response == "yes"
}
