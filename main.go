package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/ui"
)

func main() {
	m, err := ui.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, "lazysql:", err)
		os.Exit(1)
	}
	if _, err := tea.NewProgram(m).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "lazysql:", err)
		os.Exit(1)
	}
}
