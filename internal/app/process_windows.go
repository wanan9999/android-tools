package app

import (
	"golang.org/x/sys/windows"
	"os"
	"unsafe"
)

type processGroup struct{ handle windows.Handle }

func newProcessGroup() (*processGroup, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	return &processGroup{h}, nil
}
func (g *processGroup) attach(p *os.Process) error {
	if g == nil {
		return nil
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.AssignProcessToJobObject(g.handle, h)
}
func (g *processGroup) close() {
	if g != nil && g.handle != 0 {
		windows.CloseHandle(g.handle)
		g.handle = 0
	}
}
