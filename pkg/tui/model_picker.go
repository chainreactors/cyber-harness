package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type choiceItem struct {
	value   string
	title   string
	desc    string
	current bool
}

func (i choiceItem) FilterValue() string {
	return strings.TrimSpace(i.title + " " + i.desc + " " + i.value)
}

func (i choiceItem) Title() string { return i.title }

func (i choiceItem) Description() string {
	if i.current {
		if i.desc == "" {
			return "active"
		}
		return i.desc + "  active"
	}
	return i.desc
}

type choicePicker struct {
	list     list.Model
	selected string
	canceled bool
}

type modelPicker = choicePicker

func newChoicePicker(title string, choices []choiceItem, current string, width, height int) choicePicker {
	items := make([]list.Item, 0, len(choices))
	selected := 0
	for i, item := range choices {
		if item.title == "" {
			item.title = item.value
		}
		item.current = item.current || item.value == current
		if item.current {
			selected = i
		}
		items = append(items, item)
	}

	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = true
	styles := list.NewDefaultItemStyles()
	styles.SelectedTitle = lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(lipgloss.Color("6")).
		Foreground(lipgloss.Color("6")).
		Padding(0, 0, 0, 1)
	styles.SelectedDesc = styles.SelectedTitle.Foreground(lipgloss.Color("2"))
	delegate.Styles = styles

	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 18
	}
	m := list.New(items, delegate, width, height)
	m.Title = title
	m.SetShowStatusBar(false)
	m.SetShowHelp(true)
	m.SetFilteringEnabled(true)
	m.Select(selected)

	return choicePicker{list: m}
}

func newModelPicker(models []string, current string, width, height int) modelPicker {
	choices := make([]choiceItem, 0, len(models))
	for _, model := range models {
		choices = append(choices, choiceItem{value: model, title: model})
	}
	return newChoicePicker("models", choices, current, width, height)
}

func (m choicePicker) Init() tea.Cmd {
	return nil
}

func (m choicePicker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.list.SetSize(msg.Width, msg.Height)
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			if item, ok := m.list.SelectedItem().(choiceItem); ok {
				m.selected = item.value
			}
			return m, tea.Quit
		case "esc", "ctrl+c", "q":
			m.canceled = true
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m choicePicker) View() string {
	if len(m.list.Items()) == 0 {
		return "\nNo items.\n"
	}
	return strings.TrimRight(m.list.View(), "\n") + "\n"
}

func (m choicePicker) result() (string, bool) {
	if m.canceled || strings.TrimSpace(m.selected) == "" {
		return "", false
	}
	return m.selected, true
}

func runChoicePicker(title string, choices []choiceItem, current string, width, height int) (string, bool, error) {
	p := tea.NewProgram(newChoicePicker(title, choices, current, width, height), tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		return "", false, fmt.Errorf("picker: %w", err)
	}
	picker, ok := finalModel.(choicePicker)
	if !ok {
		return "", false, fmt.Errorf("picker returned %T", finalModel)
	}
	selected, ok := picker.result()
	return selected, ok, nil
}

func runModelPicker(models []string, current string, width, height int) (string, bool, error) {
	choices := make([]choiceItem, 0, len(models))
	for _, model := range models {
		choices = append(choices, choiceItem{value: model, title: model})
	}
	selected, ok, err := runChoicePicker("models", choices, current, width, height)
	if err != nil {
		return "", false, fmt.Errorf("model picker: %w", err)
	}
	return selected, ok, nil
}
