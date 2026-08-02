package web

import (
	types "github.com/chainreactors/aiscan/pkg/types"
	managementapi "github.com/chainreactors/aiscan/pkg/web/api"
)

func scanSnapshot(scan *types.Scan, sequence uint64) *types.ScanEvent {
	return managementapi.ScanSnapshot(scan, sequence)
}

func scanStatusEvent(scanID string, status types.ScanStatus) *types.ScanEvent {
	return managementapi.ScanStatusEvent(scanID, status)
}

func scanProgressEvent(scanID, data string) *types.ScanEvent {
	return managementapi.ScanProgressEvent(scanID, data)
}

func scanCompletedEvent(scanID string) *types.ScanEvent {
	return managementapi.ScanCompletedEvent(scanID)
}

func scanFailedEvent(scanID, message string, canceled bool) *types.ScanEvent {
	return managementapi.ScanFailedEvent(scanID, message, canceled)
}
