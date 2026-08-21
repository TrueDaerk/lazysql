package main

import (
	"flag"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/ui"
	"lazysql/internal/version"
)

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	noRestore := flag.Bool("no-restore", false, "start without restoring the last session")
	debugKeys := flag.Bool("debug-keys", false, "dump what the terminal reports for each key and exit (ctrl+q)")
	flag.Usage = usage
	flag.Parse()
	if *showVersion {
		fmt.Println("lazysql version " + version.Version)
		return
	}
	// The key report is its own tiny program: it answers "does this
	// terminal report shift+arrows at all", which no amount of staring at
	// the grid can.
	if *debugKeys {
		if _, err := tea.NewProgram(ui.NewKeyDebug()).Run(); err != nil {
			fmt.Fprintln(os.Stderr, "lazysql:", err)
			os.Exit(1)
		}
		return
	}

	// A positional argument is a local database file to open for this run
	// only — no profile is saved. The path is resolved before the TUI
	// starts so a typo is a plain command-line error instead of a modal
	// over an empty screen; the connect itself then happens in the UI.
	args := flag.Args()
	if len(args) > 1 {
		fmt.Fprintln(os.Stderr, "lazysql: at most one file can be opened")
		os.Exit(1)
	}

	m, err := ui.New(*noRestore)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lazysql:", err)
		os.Exit(1)
	}
	if len(args) == 1 {
		m, err = m.OpenFileOnStart(args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, "lazysql:", err)
			os.Exit(1)
		}
	}
	if _, err := tea.NewProgram(m).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "lazysql:", err)
		os.Exit(1)
	}
}

// usage documents the positional argument the flag package knows nothing
// about, next to the flags it prints itself.
func usage() {
	fmt.Fprintln(os.Stderr, "usage: lazysql [flags] [file]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "  file  a SQLite, DuckDB or Parquet file to open for this session only;")
	fmt.Fprintln(os.Stderr, "        nothing is saved to config.toml")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "flags:")
	flag.PrintDefaults()
}
