// Command metruchinas shows Argentine macroeconomic indicators in the
// terminal: USD/ARS quotes and the country-risk index.
package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"metruchinas/internal/tui"
)

func main() {
	p := tea.NewProgram(tui.New(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "metruchinas: %v\n", err)
		os.Exit(1)
	}
}
