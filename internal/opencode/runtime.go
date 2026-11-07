package opencode

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/kninetimmy/orch/internal/execx"
)

const (
	Executable           = "opencode2"
	RuntimeVersionPrefix = Executable + " v0.0.0-beta-"
	MinimumBetaBuild     = 18314
	MinimumVersion       = RuntimeVersionPrefix + "18314"
	StableFloor          = "2.0.0"
	VerifiedStable       = "2.2.6"
)

// Runtime binds a catalog read to the V2 command whose version was checked.
type Runtime struct {
	Executable string
	Warning    string
}

// Resolve prefers opencode2, but also supports V2 installed as opencode. Raw
// process output is never included in errors because it may contain credentials.
func Resolve(ctx context.Context, runner execx.Runner, directory string) (Runtime, error) {
	if runner == nil {
		return Runtime{}, fmt.Errorf("OpenCode V2 command runner is unavailable; retry through an initialized Orch command")
	}
	var failures []string
	for _, name := range []string{Executable, "opencode"} {
		res, err := runner.Run(ctx, execx.Cmd{Name: name, Args: []string{"--version"}, Dir: directory})
		if ctx.Err() != nil {
			return Runtime{}, fmt.Errorf("OpenCode V2 version check canceled: %w", ctx.Err())
		}
		if err != nil {
			failures = append(failures, name+" could not start")
			continue
		}
		if res.ExitCode != 0 {
			failures = append(failures, fmt.Sprintf("%s --version exited %d", name, res.ExitCode))
			continue
		}
		warning, err := checkRuntimeVersion(strings.TrimSpace(res.Stdout))
		if err != nil {
			failures = append(failures, name+": "+err.Error())
			continue
		}
		return Runtime{Executable: name, Warning: warning}, nil
	}
	return Runtime{}, fmt.Errorf("OpenCode V2 runtime unavailable: %s; install a supported V2 runtime or adjust PATH and retry", strings.Join(failures, "; "))
}

func checkRuntimeVersion(version string) (string, error) {
	text, ok := strings.CutPrefix(version, "opencode v")
	if !ok {
		text, ok = strings.CutPrefix(version, "opencode2 v")
	}
	malformed := func() (string, error) {
		return "", fmt.Errorf("OpenCode V2 runtime version output is malformed; minimum supported runtime is %s or stable %s", MinimumVersion, StableFloor)
	}
	if !ok {
		return malformed()
	}
	if buildText, beta := strings.CutPrefix(text, "0.0.0-beta-"); beta {
		if buildText == "" || strings.Trim(buildText, "0123456789") != "" {
			return malformed()
		}
		build, err := strconv.ParseUint(buildText, 10, 64)
		if err != nil {
			return malformed()
		}
		if build < MinimumBetaBuild {
			return "", fmt.Errorf("OpenCode V2 runtime is too old; minimum supported runtime is %s or stable %s", MinimumVersion, StableFloor)
		}
		if build == MinimumBetaBuild {
			return "", nil
		}
	} else {
		parts := strings.Split(text, ".")
		if len(parts) != 3 {
			return malformed()
		}
		var numbers [3]uint64
		for i, part := range parts {
			if part == "" || strings.Trim(part, "0123456789") != "" || (len(part) > 1 && part[0] == '0') {
				return malformed()
			}
			n, err := strconv.ParseUint(part, 10, 64)
			if err != nil {
				return malformed()
			}
			numbers[i] = n
		}
		if numbers[0] < 2 {
			return "", fmt.Errorf("OpenCode runtime is too old (legacy V1 is unsupported); minimum supported runtime is %s or stable %s", MinimumVersion, StableFloor)
		}
		if text == VerifiedStable {
			return "", nil
		}
	}
	return fmt.Sprintf("OpenCode runtime %s is outside the verified version baselines (%s and stable %s); behavior may differ; continuing required contract checks", text, MinimumVersion, VerifiedStable), nil
}
