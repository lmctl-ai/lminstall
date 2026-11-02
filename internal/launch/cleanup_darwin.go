//go:build darwin

package launch

import (
	"errors"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

type bsdProcessInfo struct {
	Flags, Status, XStatus, PID, PPID, UID, GID, RUID, RGID, SVUID, SVGID, RFU uint32
	Comm                                                                       [16]byte
	Name                                                                       [32]byte
	NFiles, PGID, PJobC, ETDev, ETPGID                                         uint32
	Nice                                                                       int32
	StartSeconds, StartMicroseconds                                            uint64
}

func cleanupGatewayProcess(record gatewayRecord, home string) error {
	queue, e := syscall.Kqueue()
	if e != nil {
		return errors.New("gateway_wait_handle_unavailable")
	}
	defer syscall.Close(queue)
	event := syscall.Kevent_t{Ident: uint64(record.PID), Filter: syscall.EVFILT_PROC, Flags: syscall.EV_ADD | syscall.EV_ONESHOT, Fflags: syscall.NOTE_EXIT}
	if _, e = syscall.Kevent(queue, []syscall.Kevent_t{event}, nil, nil); e != nil {
		if e == syscall.ESRCH {
			return nil
		}
		return errors.New("gateway_wait_registration_failed")
	}
	var info bsdProcessInfo
	n, _, errno := syscall.Syscall6(336, 2, uintptr(record.PID), 3, 0, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
	if errno == syscall.ESRCH {
		return nil
	}
	if errno != 0 || n != unsafe.Sizeof(info) {
		return errors.New("gateway_identity_unreadable")
	}
	start := math.RoundToEven((float64(info.StartSeconds) + float64(info.StartMicroseconds)/1000000) * 100)
	if start != record.Start || info.UID != uint32(os.Getuid()) {
		return errors.New("gateway_pid_reused_refused")
	}
	cmd := exec.Command("ps", "eww", "-p", strconv.Itoa(record.PID), "-o", "command=")
	b, e := cmd.Output()
	if e != nil || !strings.Contains(string(b), "HERMES_HOME="+home) || !gatewayCommand(string(b)) {
		return errors.New("foreign_gateway_process_refused")
	}
	if e = syscall.Kill(record.PID, syscall.SIGKILL); e != nil && e != syscall.ESRCH {
		return errors.New("gateway_signal_failed")
	}
	events := make([]syscall.Kevent_t, 1)
	for {
		_, e = syscall.Kevent(queue, nil, events, nil)
		if e == syscall.EINTR {
			continue
		}
		break
	}
	if e != nil {
		return errors.New("gateway_wait_failed")
	}
	return nil
}
