//go:build windows

package launch

// ExecuteOwned waits for the private job's active-process-zero completion event;
// a native gateway cannot break away from that job. No additional PID is signaled.
func cleanupGatewayProcess(record gatewayRecord, home string) error { return nil }
