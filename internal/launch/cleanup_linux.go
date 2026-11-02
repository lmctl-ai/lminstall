//go:build linux

package launch

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

func cleanupGatewayProcess(record gatewayRecord, home string) error {
	fd, _, errno := syscall.Syscall(434, uintptr(record.PID), 0, 0) // pidfd_open: exact process identity
	if errno == syscall.ESRCH {
		return nil
	}
	if errno != 0 {
		return errors.New("gateway_pid_handle_unavailable")
	}
	defer syscall.Close(int(fd))
	prefix := fmt.Sprintf("/proc/%d/", record.PID)
	b, e := os.ReadFile(prefix + "stat")
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return errors.New("gateway_identity_unreadable")
	}
	end := strings.LastIndex(string(b), ")")
	if end < 0 {
		return errors.New("gateway_identity_invalid")
	}
	fields := strings.Fields(string(b)[end+1:])
	if len(fields) < 20 {
		return errors.New("gateway_identity_invalid")
	}
	start, e := strconv.ParseFloat(fields[19], 64)
	if e != nil || start != record.Start {
		return errors.New("gateway_pid_reused_refused")
	}
	b, e = os.ReadFile(prefix + "environ")
	if e != nil {
		return errors.New("gateway_environment_unreadable")
	}
	matched := false
	for _, item := range strings.Split(string(b), "\x00") {
		if strings.HasPrefix(item, "HERMES_HOME=") {
			canonical, _ := filepath.EvalSymlinks(strings.TrimPrefix(item, "HERMES_HOME="))
			want, _ := filepath.EvalSymlinks(home)
			matched = canonical == want
		}
	}
	command, e := os.ReadFile(prefix + "cmdline")
	if e != nil || !matched || !gatewayCommand(strings.ReplaceAll(string(command), "\x00", " ")) {
		return errors.New("foreign_gateway_process_refused")
	}
	_, _, errno = syscall.Syscall6(424, fd, uintptr(syscall.SIGKILL), 0, 0, 0, 0) // pidfd_send_signal
	if errno != 0 && errno != syscall.ESRCH {
		return errors.New("gateway_signal_failed")
	}
	poll := struct {
		FD              int32
		Events, Revents int16
	}{int32(fd), 1, 0}
	for {
		_, _, errno = syscall.Syscall6(syscall.SYS_PPOLL, uintptr(unsafe.Pointer(&poll)), 1, 0, 0, 0, 0)
		if errno == syscall.EINTR {
			continue
		}
		break
	}
	if errno != 0 {
		return errors.New("gateway_wait_failed")
	}
	return nil
}
