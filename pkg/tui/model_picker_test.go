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
