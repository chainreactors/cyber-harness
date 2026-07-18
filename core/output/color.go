package output

import (
	"github.com/chainreactors/logs"
	"github.com/chainreactors/utils/parsers"
)

const (
	ANSIReset  = "\033[0m"
	ANSIBold   = "\033[1m"
	ANSIDim    = "\033[2m"
	ANSIRed    = "\033[31m"
	ANSIGreen  = "\033[32m"
	ANSIYellow = "\033[33m"
	ANSICyan   = "\033[36m"
)

type Color struct {
	Enabled bool
}

func NewColor(enabled bool) Color {
	return Color{Enabled: enabled}
}

func (c Color) Code(code string) string {
	if !c.Enabled {
		return ""
	}
	return code
}

func (c Color) Wrap(s, code string) string {
	if !c.Enabled {
		return s
	}
	return code + s + ANSIReset
}

func (c Color) Green(s string) string {
	if !c.Enabled {
		return s
	}
	return logs.Green(s)
}

func (c Color) GreenBold(s string) string {
	if !c.Enabled {
		return s
	}
	return logs.GreenBold(s)
}

func (c Color) Red(s string) string {
	if !c.Enabled {
		return s
	}
	return logs.Red(s)
}

func (c Color) Yellow(s string) string {
	if !c.Enabled {
		return s
	}
	return logs.Yellow(s)
}

func (c Color) YellowBold(s string) string {
	if !c.Enabled {
		return s
	}
	return logs.YellowBold(s)
}

func (c Color) Cyan(s string) string {
	if !c.Enabled {
		return s
	}
	return logs.Cyan(s)
}

func (c Color) Bold(s string) string {
	if !c.Enabled {
		return s
	}
	return ANSIBold + s + ANSIReset
}

func (c Color) Dim(s string) string {
	if !c.Enabled {
		return s
	}
	return ANSIDim + s + ANSIReset
}

func (c Color) Status(s string) string {
	if !c.Enabled {
		return s
	}
	return parsers.RenderStatus(s)
}

func (c Color) ForPriority(p string) func(string) string {
	if !c.Enabled {
		return func(s string) string { return s }
	}
	switch p {
	case "low":
		return logs.Cyan
	case "medium":
		return logs.Yellow
	case "high":
		return logs.Red
	case "critical":
		return logs.RedBold
	default:
		return c.Dim
	}
}
