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
	flag.Parse()
	if *showVersion {
		fmt.Println("lazysql version " + version.Version)
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
