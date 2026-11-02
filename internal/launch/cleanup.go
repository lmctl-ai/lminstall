package launch

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type gatewayRecord struct {
	Kind  string  `json:"kind"`
	Home  string  `json:"hermes_home"`
	PID   int     `json:"pid"`
	Start float64 `json:"start_time"`
}

func validateGatewayRecord(record gatewayRecord, home string) error {
	expected, e := filepath.EvalSymlinks(home)
	if e != nil {
		return errors.New("gateway_home_unavailable")
	}
	got, e := filepath.EvalSymlinks(record.Home)
	if e != nil || got != expected || record.Kind != "hermes-gateway" || record.PID <= 0 || record.Start <= 0 {
		return errors.New("foreign_gateway_record_refused")
	}
	return nil
}
func cleanupHermes(home string) error {
	path := filepath.Join(home, "gateway.pid")
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return errors.New("gateway_record_unreadable")
	}
	var record gatewayRecord
	if json.Unmarshal(b, &record) != nil {
		return errors.New("gateway_record_invalid")
	}
	if e = validateGatewayRecord(record, home); e != nil {
		return e
	}
	if e = cleanupGatewayProcess(record, home); e != nil {
		return e
	}
	os.Remove(path)
	return nil
}
func gatewayCommand(s string) bool {
	// Command words, not a process-name search. HOME is separately validated.
	words := strings.Fields(s)
	gateway, run := false, false
	for _, word := range words {
		if word == "gateway" || strings.HasSuffix(word, "gateway/run.py") {
			gateway = true
		}
		if word == "run" || strings.HasSuffix(word, "gateway/run.py") {
			run = true
		}
	}
	return gateway && run
}
