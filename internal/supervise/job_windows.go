//go:build windows

package supervise

import (
	"syscall"
	"unsafe"
)

const (
	jobObjectLimitKillOnJobClose      = 0x2000
	jobObjectExtendedLimitInformation = 9
)

type basicLimits struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type extendedLimits struct {
	Basic                 basicLimits
	IO                    [6]uint64
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

func TieChildrenToSupervisor() error {
	kernel := syscall.NewLazyDLL("kernel32.dll")
	job, _, err := kernel.NewProc("CreateJobObjectW").Call(0, 0)
	if job == 0 {
		return err
	}
	var limits extendedLimits
	limits.Basic.LimitFlags = jobObjectLimitKillOnJobClose
	ok, _, err := kernel.NewProc("SetInformationJobObject").Call(job, jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits))
	if ok == 0 {
		return err
	}
	self, err := syscall.GetCurrentProcess()
	if err != nil {
		return err
	}
	ok, _, err = kernel.NewProc("AssignProcessToJobObject").Call(job, uintptr(self))
	if ok == 0 {
		return err
	}
	return nil
}

func HideConsole() {
	kernel := syscall.NewLazyDLL("kernel32.dll")
	window, _, _ := kernel.NewProc("GetConsoleWindow").Call()
	if window == 0 {
		return
	}
	syscall.NewLazyDLL("user32.dll").NewProc("ShowWindow").Call(window, 0)
}
