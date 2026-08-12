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

	m, err := ui.New(*noRestore)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lazysql:", err)
		os.Exit(1)
	}
	if _, err := tea.NewProgram(m).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "lazysql:", err)
		os.Exit(1)
	}
}
