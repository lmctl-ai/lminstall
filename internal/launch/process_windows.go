//go:build windows

package launch

import (
	"github.com/lmctl-ai/lminstall/internal/install"
	"os/exec"
)

func executeRuntime(cmd *exec.Cmd) error { return install.ExecuteOwned(cmd) }
