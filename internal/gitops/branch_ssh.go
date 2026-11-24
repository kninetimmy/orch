package gitops

import (
	"fmt"
	"path/filepath"
	"strings"
)

func boundSSHCommand() (string, error) {
	executable, nullConfig, err := systemOpenSSH()
	if err != nil || !filepath.IsAbs(executable) {
		return "", fmt.Errorf("could not locate system OpenSSH for bound cleanup; preserve the checkouts and branches")
	}
	// Git requires a shell command for SSH options. Quote only the OS path;
	// Git supplies the validated destination as arguments, not shell text.
	quoted := "'" + strings.ReplaceAll(filepath.ToSlash(executable), "'", "'\\''") + "'"
	// An empty config excludes HostName (and Match exec) in both user and
	// system files. Default known_hosts, identity files and agent still work.
	return quoted + " -F " + nullConfig + " -o BatchMode=yes -o StrictHostKeyChecking=yes" +
		" -o CanonicalizeHostname=no -o ProxyCommand=none -o ProxyJump=none" +
		" -o ControlMaster=no -o ControlPath=none -o CheckHostIP=no -o VerifyHostKeyDNS=no -o UpdateHostKeys=no", nil
}
