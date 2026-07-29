//go:build !windows

package persistence

import (
	"fmt"
	"os"
	"syscall"

	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// acquireAdvisoryLock takes an OS advisory exclusive lock on lockPath and holds
// it for the lifetime of the returned release func. The kernel drops a flock
// automatically when the owning process dies, so an abrupt crash (SIGKILL, panic,
// power loss) never leaves an un-acquirable lock behind the way an O_EXCL marker
// file did (#56 Standards P1-1). The lock file is left on disk as the stable lock
// anchor; only the advisory lock, not the file's existence, gates entry.
func acquireAdvisoryLock(lockPath, unavailableMessage string) (func(), error) {
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			return nil, fmt.Errorf("%w: %s", ports.ErrDependencyUnavailable, unavailableMessage)
		}
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}
