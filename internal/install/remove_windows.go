//go:build windows

package install

import (
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

var setFileInfo = kernel.NewProc("SetFileInformationByHandle")

// Modern Windows POSIX disposition removes the owned namespace entry without
// waiting for a cached image section to disappear. Existing handles keep their
// bytes until closed; no foreign process is stopped and no retry timer is used.
func unlinkManaged(path string) error {
	name, e := syscall.UTF16PtrFromString(path)
	if e != nil {
		return e
	}
	h, e := syscall.CreateFile(name, 0x10000, 1|2|4, nil, 3, 0x02000000|0x00200000, 0) // DELETE, full sharing, backup/reparse semantics
	if e != nil {
		return &os.PathError{Op: "open for owned deletion", Path: path, Err: e}
	}
	flags := uint32(1 | 2 | 0x10) // DELETE | POSIX_SEMANTICS | IGNORE_READONLY_ATTRIBUTE; no FORCE_IMAGE_SECTION_CHECK
	ok, _, callErr := setFileInfo.Call(uintptr(h), 21, uintptr(unsafe.Pointer(&flags)), unsafe.Sizeof(flags))
	syscall.CloseHandle(h)
	if ok == 0 {
		return &os.PathError{Op: "owned POSIX deletion", Path: path, Err: callErr}
	}
	return nil
}
func removeManaged(path string) error {
	st, e := os.Lstat(path)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if st.IsDir() && st.Mode()&os.ModeSymlink == 0 {
		children, e := os.ReadDir(path)
		if e != nil {
			return e
		}
		for _, child := range children {
			if e = removeManaged(filepath.Join(path, child.Name())); e != nil {
				return e
			}
		}
	}
	if e = unlinkManaged(path); e == nil {
		return nil
	}
	// Older filesystems/platforms still get their normal truthful refusal.
	return os.Remove(path)
}
