//go:build windows

package assets

import (
	"os"
	"syscall"
	"unsafe"
)

const (
	lockfileFailImmediately = 0x00000001
	lockfileExclusiveLock   = 0x00000002
	errorLockViolation      = syscall.Errno(33)
)

var (
	kernel32DLL      = syscall.NewLazyDLL("kernel32.dll")
	lockFileExProc   = kernel32DLL.NewProc("LockFileEx")
	unlockFileExProc = kernel32DLL.NewProc("UnlockFileEx")
)

func tryLockFile(file *os.File) (bool, error) {
	overlapped := new(syscall.Overlapped)
	result, _, callErr := lockFileExProc.Call(
		file.Fd(),
		lockfileExclusiveLock|lockfileFailImmediately,
		0,
		1,
		0,
		uintptr(unsafe.Pointer(overlapped)),
	)
	if result != 0 {
		return true, nil
	}
	if callErr == errorLockViolation {
		return false, nil
	}
	return false, callErr
}

func unlockFile(file *os.File) error {
	overlapped := new(syscall.Overlapped)
	result, _, callErr := unlockFileExProc.Call(
		file.Fd(),
		0,
		1,
		0,
		uintptr(unsafe.Pointer(overlapped)),
	)
	if result == 0 {
		return callErr
	}
	return nil
}
