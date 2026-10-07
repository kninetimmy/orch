//go:build windows

package claudenative

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"
)

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObject      = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJob    = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJob   = kernel32.NewProc("AssignProcessToJobObject")
	procTerminateJobObject   = kernel32.NewProc("TerminateJobObject")
	procNtResumeProcess      = syscall.NewLazyDLL("ntdll.dll").NewProc("NtResumeProcess")
	errProcessTreeUnverified = fmt.Errorf("%w: process tree containment unavailable", ErrUnavailable)
)

const (
	createSuspended      = 0x00000004
	killOnJobClose       = 0x00002000
	extendedLimitInfo    = 9
	processSetQuota      = 0x0100
	processTerminate     = 0x0001
	processSuspendResume = 0x0800
)

// jobLimits is JOBOBJECT_EXTENDED_LIMIT_INFORMATION. A layout mismatch makes
// SetInformationJobObject fail, which refuses the launch.
type jobLimits struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
	IoInfo                  [6]uint64
	ProcessMemoryLimit      uintptr
	JobMemoryLimit          uintptr
	PeakProcessMemoryUsed   uintptr
	PeakJobMemoryUsed       uintptr
}

// processTree is a kill-on-close job object. The child starts suspended and is
// assigned before it runs, so an npm .cmd shim, Claude Code and every tool
// process it starts are in the job and end with it.
type processTree struct{ job uintptr }

func startTree(cmd *exec.Cmd) (*processTree, error) {
	job, _, err := procCreateJobObject.Call(0, 0)
	if job == 0 {
		return nil, errors.Join(errProcessTreeUnverified, err)
	}
	t := &processTree{job: job}
	limits := jobLimits{LimitFlags: killOnJobClose}
	if ok, _, err := procSetInformationJob.Call(job, extendedLimitInfo, uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits)); ok == 0 {
		t.close()
		return nil, errors.Join(errProcessTreeUnverified, err)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createSuspended, HideWindow: true}
	if err := cmd.Start(); err != nil {
		t.close()
		return nil, err
	}
	handle, err := syscall.OpenProcess(processSetQuota|processTerminate|processSuspendResume, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = cmd.Process.Kill()
		t.close()
		return nil, errors.Join(errProcessTreeUnverified, err)
	}
	defer func() { _ = syscall.CloseHandle(handle) }()
	if ok, _, err := procAssignProcessToJob.Call(job, uintptr(handle)); ok == 0 {
		_ = cmd.Process.Kill()
		t.close()
		return nil, errors.Join(errProcessTreeUnverified, err)
	}
	if status, _, _ := procNtResumeProcess.Call(uintptr(handle)); status != 0 {
		_ = t.kill()
		t.close()
		return nil, errProcessTreeUnverified
	}
	return t, nil
}

func (t *processTree) kill() error {
	if ok, _, err := procTerminateJobObject.Call(t.job, 1); ok == 0 {
		return err
	}
	return nil
}

// close releases the job; kill-on-close ends anything still inside it.
func (t *processTree) close() { _ = syscall.CloseHandle(syscall.Handle(t.job)) }
