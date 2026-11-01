//go:build !windows

package install

import "os"

func removeManaged(path string) error { return os.RemoveAll(path) }
