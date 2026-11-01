//go:build windows

package install

// A version command must not leave its own bootstrap workers keeping binaries
// open. Start suspended, assign a private job, then resume. On parent exit,
// terminate only that job's descendants and wait for the active-process-zero
// completion event. No process-name matching, polling, sleeps or deadlines.
import (
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"
)

var kernel = syscall.NewLazyDLL("kernel32.dll")
var createJob = kernel.NewProc("CreateJobObjectW")
var setJob = kernel.NewProc("SetInformationJobObject")
var assignJob = kernel.NewProc("AssignProcessToJobObject")
var terminateJob = kernel.NewProc("TerminateJobObject")
var createPort = kernel.NewProc("CreateIoCompletionPort")
var completion = kernel.NewProc("GetQueuedCompletionStatus")
var snapshotThreads = kernel.NewProc("CreateToolhelp32Snapshot")
var threadFirst = kernel.NewProc("Thread32First")
var threadNext = kernel.NewProc("Thread32Next")
var openThread = kernel.NewProc("OpenThread")
var resumeThread = kernel.NewProc("ResumeThread")
var waitProcess = kernel.NewProc("WaitForSingleObject")

type basicLimits struct {
	ProcessTime, JobTime int64
	Flags                uint32
	MinSet, MaxSet       uintptr
	Active               uint32
	Affinity             uintptr
	Priority, Scheduling uint32
}
type ioCounters struct{ ReadOps, WriteOps, OtherOps, ReadBytes, WriteBytes, OtherBytes uint64 }
type extendedLimits struct {
	Basic                                          basicLimits
	IO                                             ioCounters
	ProcessMemory, JobMemory, PeakProcess, PeakJob uintptr
}
type association struct{ Key, Port uintptr }
type threadEntry struct {
	Size, Usage, ID, PID uint32
	Base, Delta          int32
	Flags                uint32
}

func resumeOwned(pid uint32) error {
	snap, _, err := snapshotThreads.Call(4, 0)
	if snap == ^uintptr(0) {
		return err
	}
	defer syscall.CloseHandle(syscall.Handle(snap))
	entry := threadEntry{Size: uint32(unsafe.Sizeof(threadEntry{}))}
	ok, _, _ := threadFirst.Call(snap, uintptr(unsafe.Pointer(&entry)))
	for ok != 0 {
		if entry.PID == pid {
			h, _, err := openThread.Call(2, 0, uintptr(entry.ID))
			if h == 0 {
				return err
			}
			defer syscall.CloseHandle(syscall.Handle(h))
			r, _, err := resumeThread.Call(h)
			if r == 0xffffffff {
				return err
			}
			if r == 0 {
				return fmt.Errorf("owned initial thread was not suspended")
			}
			return nil
		}
		entry.Size = uint32(unsafe.Sizeof(entry))
		ok, _, _ = threadNext.Call(snap, uintptr(unsafe.Pointer(&entry)))
	}
	return fmt.Errorf("initial thread of owned process %d not found", pid)
}
func runOwned(cmd *exec.Cmd) error {
	job, _, e := createJob.Call(0, 0)
	if job == 0 {
		return fmt.Errorf("create installer job: %w", e)
	}
	defer syscall.CloseHandle(syscall.Handle(job))
	limits := extendedLimits{}
	limits.Basic.Flags = 0x2000 // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if ok, _, e := setJob.Call(job, 9, uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits)); ok == 0 {
		return fmt.Errorf("set installer job limits: %w", e)
	}
	port, _, e := createPort.Call(^uintptr(0), 0, 0, 1)
	if port == 0 {
		return e
	}
	defer syscall.CloseHandle(syscall.Handle(port))
	assoc := association{Key: 1, Port: port}
	if ok, _, e := setJob.Call(job, 7, uintptr(unsafe.Pointer(&assoc)), unsafe.Sizeof(assoc)); ok == 0 {
		return e
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= 4 // CREATE_SUSPENDED
	if e := cmd.Start(); e != nil {
		return e
	}
	process, e := syscall.OpenProcess(0x100100|1, false, uint32(cmd.Process.Pid)) // SYNCHRONIZE | SET_QUOTA | TERMINATE
	if e != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return e
	}
	defer syscall.CloseHandle(process)
	if ok, _, e := assignJob.Call(job, uintptr(process)); ok == 0 {
		cmd.Process.Kill()
		cmd.Wait()
		return fmt.Errorf("assign owned installer process to job: %w", e)
	}
	if e := resumeOwned(uint32(cmd.Process.Pid)); e != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return e
	}
	done := make(chan error, 1)
	go func() {
		wait, _, e := waitProcess.Call(uintptr(process), 0xffffffff)
		if wait != 0 {
			done <- e
			return
		}
		// Parent has already exited with its real status. Workers cannot outlive the command.
		terminateJob.Call(job, 1)
		for {
			var message uint32
			var key, overlap uintptr
			ok, _, e := completion.Call(port, uintptr(unsafe.Pointer(&message)), uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&overlap)), 0xffffffff)
			if ok == 0 {
				done <- e
				return
			}
			if key == 1 && message == 4 {
				done <- nil
				return
			}
		}
	}()
	err := cmd.Wait()
	if waitErr := <-done; waitErr != nil && err == nil {
		return waitErr
	}
	return err
}
