package ipquality

import (
	"os/exec"
	"syscall"
)

// requiredCommands are the runtime commands the ip.sh script needs on PATH.
var requiredCommands = []string{"jq", "curl", "bc", "nc", "dig", "ip"}

// MissingDependencies lists the script runtime commands absent from PATH.
func MissingDependencies() []string {
	var missing []string
	for _, command := range requiredCommands {
		if _, err := exec.LookPath(command); err != nil {
			missing = append(missing, command)
		}
	}
	return missing
}

// killProcessGroup SIGKILLs the process group of a started command; both the
// script runner and the dependency installer launch their children in their
// own group so a timeout cannot leave grandchildren behind.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
