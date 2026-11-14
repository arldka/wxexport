package tui

import "charm.land/lipgloss/v2"

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("13"))
	subtleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	okStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Bold(true)
	helpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	sentStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	receivedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	dateSepStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)
)
