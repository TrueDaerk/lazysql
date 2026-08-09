package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/ui"
)

func main() {
	if _, err := tea.NewProgram(ui.New()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "lazysql:", err)
		os.Exit(1)
	}
}
