package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestModelPickerSelectsCurrentModel(t *testing.T) {
	model := newModelPicker([]string{"model-a", "model-b"}, "model-b", 80, 20)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	picker, ok := updated.(modelPicker)
	if !ok {
		t.Fatalf("updated model = %T, want modelPicker", updated)
	}
	selected, ok := picker.result()
	if !ok || selected != "model-b" {
		t.Fatalf("selected = %q ok=%v, want model-b true", selected, ok)
	}
}

func TestChoicePickerSelectsSession(t *testing.T) {
	model := newChoicePicker("sessions", []choiceItem{
		{value: "session-new.json", title: "session-new.json"},
		{value: "session-old.json", title: "session-old.json"},
	}, "", 80, 20)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	picker, ok := updated.(choicePicker)
	if !ok {
		t.Fatalf("updated model = %T, want choicePicker", updated)
	}
	selected, ok := picker.result()
	if !ok || selected != "session-old.json" {
		t.Fatalf("selected = %q ok=%v, want session-old.json true", selected, ok)
	}
}
