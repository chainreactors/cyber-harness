package app

// ScannerState reports the profile-owned scanner through its business API.
func (a *App) ScannerState() string {
	if a == nil {
		return "unavailable"
	}
	if a.scanner == nil {
		return "disabled"
	}
	return a.scanner.State()
}
