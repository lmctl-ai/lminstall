//go:build windows

package launch

import (
	"encoding/binary"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var ntQueryProcess = syscall.NewLazyDLL("ntdll.dll").NewProc("NtQueryInformationProcess")
var readMemory = kernel.NewProc("ReadProcessMemory")
var queryMemory = kernel.NewProc("VirtualQueryEx")
var terminateProcess = kernel.NewProc("TerminateProcess")

func processBirth(handle syscall.Handle) (float64, error) {
	var created, exited, kern, user syscall.Filetime
	if e := syscall.GetProcessTimes(handle, &created, &exited, &kern, &user); e != nil {
		return 0, e
	}
	return math.RoundToEven(float64(created.Nanoseconds()) / 1e7), nil
}
func remoteBytes(handle syscall.Handle, address uintptr, n int) ([]byte, error) {
	b := make([]byte, n)
	var read uintptr
	if n == 0 {
		return b, nil
	}
	ok, _, e := readMemory.Call(uintptr(handle), address, uintptr(unsafe.Pointer(&b[0])), uintptr(n), uintptr(unsafe.Pointer(&read)))
	if ok == 0 || read != uintptr(n) {
		return nil, e
	}
	return b, nil
}
func remotePointer(handle syscall.Handle, address uintptr) (uintptr, error) {
	b, e := remoteBytes(handle, address, 8)
	if e != nil {
		return 0, e
	}
	return uintptr(binary.LittleEndian.Uint64(b)), nil
}
func processEnvironment(handle syscall.Handle) (map[string]string, error) {
	// Supported Windows targets and owned native Python are 64 bit. Offsets are
	// the public PEB/RTL_USER_PROCESS_PARAMETERS layout, queried on this handle.
	var basic [6]uintptr
	status, _, _ := ntQueryProcess.Call(uintptr(handle), 0, uintptr(unsafe.Pointer(&basic)), unsafe.Sizeof(basic), 0)
	if status != 0 {
		return nil, errors.New("gateway_process_info_unavailable")
	}
	parameters, e := remotePointer(handle, basic[1]+0x20)
	if e != nil {
		return nil, e
	}
	address, e := remotePointer(handle, parameters+0x80)
	if e != nil {
		return nil, e
	}
	var units []uint16
	for {
		var region struct {
			Base, AllocationBase uintptr
			Protection           uint32
			_                    uint32
			Size                 uintptr
			State, Protect, Type uint32
			_                    uint32
		}
		n, _, e := queryMemory.Call(uintptr(handle), address, uintptr(unsafe.Pointer(&region)), unsafe.Sizeof(region))
		if n == 0 || region.Size == 0 || region.Base+region.Size <= address {
			return nil, e
		}
		b, e := remoteBytes(handle, address, int(region.Base+region.Size-address))
		if e != nil {
			return nil, e
		}
		for i := 0; i+1 < len(b); i += 2 {
			u := binary.LittleEndian.Uint16(b[i:])
			units = append(units, u)
			if u == 0 && len(units) > 1 && units[len(units)-2] == 0 {
				env := map[string]string{}
				for _, item := range strings.Split(string(utf16.Decode(units)), "\x00") {
					if k, v, ok := strings.Cut(item, "="); ok {
						env[strings.ToUpper(k)] = v
					}
				}
				return env, nil
			}
		}
		address = region.Base + region.Size
	}
}
func processCommand(handle syscall.Handle) (string, error) {
	var needed uint32
	ntQueryProcess.Call(uintptr(handle), 60, 0, 0, uintptr(unsafe.Pointer(&needed)))
	if needed == 0 {
		return "", errors.New("gateway_command_unavailable")
	}
	b := make([]byte, needed)
	status, _, _ := ntQueryProcess.Call(uintptr(handle), 60, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), uintptr(unsafe.Pointer(&needed)))
	if status != 0 || len(b) < 16 {
		return "", errors.New("gateway_command_unavailable")
	}
	size := int(binary.LittleEndian.Uint16(b))
	pointer := uintptr(binary.LittleEndian.Uint64(b[8:]))
	base := uintptr(unsafe.Pointer(&b[0]))
	if pointer < base || pointer+uintptr(size) > base+uintptr(len(b)) {
		return "", errors.New("gateway_command_invalid")
	}
	offset := int(pointer - base)
	units := make([]uint16, size/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(b[offset+i*2:])
	}
	return string(utf16.Decode(units)), nil
}
func cleanupGatewayProcess(record gatewayRecord, home string) error {
	handle, e := syscall.OpenProcess(0x100000|0x400|0x10|1, false, uint32(record.PID))
	if e == syscall.Errno(87) {
		return nil
	}
	if e != nil {
		return errors.New("gateway_process_handle_unavailable")
	}
	defer syscall.CloseHandle(handle)
	if status, _ := syscall.WaitForSingleObject(handle, 0); status == syscall.WAIT_OBJECT_0 {
		return nil
	}
	birth, e := processBirth(handle)
	if e != nil || birth != record.Start {
		return errors.New("gateway_pid_reused_refused")
	}
	env, e := processEnvironment(handle)
	if e != nil {
		return errors.New("gateway_environment_unreadable")
	}
	got, e := filepath.EvalSymlinks(env["HERMES_HOME"])
	want, e2 := filepath.EvalSymlinks(home)
	command, e3 := processCommand(handle)
	if e != nil || e2 != nil || !strings.EqualFold(got, want) || e3 != nil || !gatewayCommand(command) {
		return errors.New("foreign_gateway_process_refused")
	}
	if ok, _, _ := terminateProcess.Call(uintptr(handle), 1); ok == 0 {
		return errors.New("gateway_signal_failed")
	}
	if status, e := syscall.WaitForSingleObject(handle, syscall.INFINITE); e != nil || status != syscall.WAIT_OBJECT_0 {
		return errors.New("gateway_wait_failed")
	}
	return nil
}
