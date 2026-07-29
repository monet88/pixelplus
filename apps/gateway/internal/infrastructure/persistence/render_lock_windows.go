//go:build windows

package persistence

import (
	"errors"
	"fmt"
	"syscall"

	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// errorSharingViolation is the Win32 ERROR_SHARING_VIOLATION (32) returned when
// another process already holds the exclusive (share-mode 0) handle.
const errorSharingViolation = syscall.Errno(32)

// acquireAdvisoryLock opens lockPath with an exclusive share mode (dwShareMode
// = 0) so a second opener fails with a sharing violation while this handle is
// held. Windows closes the handle automatically when the owning process dies,
// so an abrupt crash never leaves an un-acquirable lock behind the way an
// O_EXCL marker file did (#56 Standards P1-1). This is the Windows analogue of
// the Unix flock path and adds no dependency beyond the standard library.
func acquireAdvisoryLock(lockPath, unavailableMessage string) (func(), error) {
	namePtr, err := syscall.UTF16PtrFromString(lockPath)
	if err != nil {
		return nil, err
	}
	handle, err := syscall.CreateFile(
		namePtr,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		0, // exclusive: no sharing while held
		nil,
		syscall.OPEN_ALWAYS,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		if errors.Is(err, errorSharingViolation) {
			return nil, fmt.Errorf("%w: %s", ports.ErrDependencyUnavailable, unavailableMessage)
		}
		return nil, err
	}
	return func() { _ = syscall.CloseHandle(handle) }, nil
}
