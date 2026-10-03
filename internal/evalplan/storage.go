package evalplan

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/paths"
)

var errLinkedFile = errors.New("linked file is not an independent local artifact")

// Reject ambiguous path syntax before cleaning or accessing any component.
// Absolute local drive paths are supported; UNC/device namespaces are not.
func localPath(base, name string, absolute bool) (string, error) {
	if name == "" || len(name) > 4096 || strings.HasPrefix(name, `\\`) || strings.HasPrefix(name, "//") {
		return "", fmt.Errorf("require a bounded local path, not a network/device path")
	}
	if absolute && !filepath.IsAbs(name) {
		return "", fmt.Errorf("require an explicit absolute local path: %q", name)
	}
	volume := filepath.VolumeName(name)
	if volume != "" && (runtime.GOOS != "windows" || len(volume) != 2 || volume[1] != ':' || !filepath.IsAbs(name)) {
		return "", fmt.Errorf("ambiguous local volume path: %q", name)
	}
	rest := strings.TrimPrefix(name, volume)
	if runtime.GOOS == "windows" {
		rest = strings.ReplaceAll(rest, `\`, "/")
	} else if strings.Contains(rest, `\`) {
		return "", fmt.Errorf("backslash path aliases are forbidden: %q", name)
	}
	rest = strings.TrimPrefix(rest, "./")
	for _, segment := range strings.Split(rest, "/") {
		if segment == "" {
			continue
		}
		if segment == "." || segment == ".." || strings.TrimRight(segment, " .") != segment || strings.ContainsAny(segment, `<>:"|?*~`) {
			return "", fmt.Errorf("traversing or ambiguous path component %q", segment)
		}
		for _, ch := range segment {
			if ch < 32 || ch == 127 {
				return "", fmt.Errorf("control characters are forbidden in local paths")
			}
		}
		device := strings.ToLower(strings.Split(segment, ".")[0])
		if slices.Contains([]string{"con", "prn", "aux", "nul", "com1", "com2", "com3", "com4", "com5", "com6", "com7", "com8", "com9", "lpt1", "lpt2", "lpt3", "lpt4", "lpt5", "lpt6", "lpt7", "lpt8", "lpt9", "com¹", "com²", "com³", "lpt¹", "lpt²", "lpt³"}, device) {
			return "", fmt.Errorf("reserved device path component %q", segment)
		}
	}
	if !filepath.IsAbs(name) {
		name = filepath.Join(base, name)
	}
	return filepath.Abs(name)
}

func directory(name string, allowMissing bool) (string, error) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return "", fmt.Errorf("evaluation local path verification unavailable on %s", runtime.GOOS)
	}
	if err := verifyDriveRoot(name); err != nil {
		return "", err
	}
	for p := name; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil {
			if !allowMissing || !errors.Is(err, fs.ErrNotExist) {
				return "", fmt.Errorf("verify directory %s: %w", p, err)
			}
		} else if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 || reparse(info) {
			return "", fmt.Errorf("linked/reparse/non-directory path forbidden: %s", p)
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	canonical, err := paths.Canonical(name)
	if err != nil {
		return "", err
	}
	same := canonical == name
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		same = strings.EqualFold(canonical, name)
	}
	if !same {
		return "", fmt.Errorf("directory resolved through an alias: %s", name)
	}
	return canonical, nil
}

// Anchor each directory component and compare its opened identity with Lstat.
// A link substituted between path validation and opening cannot redirect writes.
func openDirectory(name string) (*os.Root, error) {
	volume := filepath.VolumeName(name) + string(filepath.Separator)
	root, err := os.OpenRoot(volume)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(volume, name)
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	if rel == "." {
		return root, nil
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		info, err := root.Lstat(part)
		if err != nil || !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 || reparse(info) {
			_ = root.Close()
			return nil, fmt.Errorf("directory component linked or unverifiable: %s: %v", part, err)
		}
		child, err := root.OpenRoot(part)
		_ = root.Close()
		if err != nil {
			return nil, err
		}
		opened, err := child.Stat(".")
		if err != nil || !os.SameFile(info, opened) {
			_ = child.Close()
			return nil, fmt.Errorf("directory identity changed: %s: %v", part, err)
		}
		root = child
	}
	return root, nil
}

func readRoot(root *os.Root, name string, limit int) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || reparse(info) {
		return nil, fmt.Errorf("linked/reparse/nonregular file forbidden: %s", name)
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("file identity changed or unverifiable: %s: %v", name, err)
	}
	links, err := linkCount(f)
	if err != nil {
		return nil, fmt.Errorf("verify file aliases %s: %w", name, err)
	}
	if links != 1 {
		return nil, fmt.Errorf("%w: %s", errLinkedFile, name)
	}
	if opened.Size() > int64(limit) {
		return nil, fmt.Errorf("%s exceeds %d-byte input limit", name, limit)
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("%s exceeds %d-byte input limit", name, limit)
	}
	return data, nil
}

func readLocal(base, name string, limit int) (string, []byte, error) {
	name, err := localPath(base, name, false)
	if err != nil {
		return "", nil, err
	}
	parent, err := directory(filepath.Dir(name), false)
	if err != nil {
		return "", nil, err
	}
	root, err := openDirectory(parent)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = root.Close() }()
	data, err := readRoot(root, filepath.Base(name), limit)
	if err != nil {
		return "", nil, fmt.Errorf("read local artifact %s: %w", name, err)
	}
	canonical, err := paths.Canonical(name)
	if err != nil {
		return "", nil, err
	}
	same, err := samePath(parent, filepath.Dir(canonical))
	if err != nil || !same {
		return "", nil, fmt.Errorf("artifact resolved through a directory alias: %s: %v", name, err)
	}
	return canonical, data, nil
}

func effectiveConfiguration(repo string) (Configuration, error) {
	_, data, err := readLocal(repo, config.Path, MaxPlanBytes)
	if err != nil {
		return Configuration{}, fmt.Errorf("effective configuration: %w", err)
	}
	cfg, err := config.Parse(data)
	if err != nil {
		return Configuration{}, err
	}
	_, overlay, err := readLocal(repo, config.LocalOverridePath, MaxPlanBytes)
	if err == nil {
		cfg, err = config.MergeLocal(cfg, overlay)
	} else if errors.Is(err, fs.ErrNotExist) {
		err = nil
	}
	if err != nil {
		return Configuration{}, fmt.Errorf("effective local configuration: %w", err)
	}
	return configuration(cfg)
}

// Built-in read-only Git verbs only. Disable lazy object fetching and replace
// objects so a missing pin stays a local error, never a remote request.
func gitRead(ctx context.Context, runner execx.Runner, repo string, args ...string) (string, error) {
	if runner == nil {
		runner = execx.Local{}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := runner.Run(ctx, execx.Cmd{Name: "git", Args: append([]string{"--no-optional-locks", "--no-pager", "-c", "core.fsmonitor=false"}, args...), Dir: repo, Env: []string{"GIT_TERMINAL_PROMPT=0", "GIT_NO_REPLACE_OBJECTS=1", "GIT_NO_LAZY_FETCH=1", "LC_ALL=C"}})
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("local git %s exited %d", args[0], res.ExitCode)
	}
	if len(res.Stdout) > MaxArtifactBytes {
		return "", fmt.Errorf("local Git metadata exceeds %d bytes", MaxArtifactBytes)
	}
	return res.Stdout, nil
}

func pathList(base string, names []string, mustExist bool) ([]string, error) {
	if names == nil || len(names) > 32 {
		return nil, fmt.Errorf("declare path arrays explicitly with at most 32 entries")
	}
	out := []string{}
	for _, name := range names {
		name, err := localPath(base, name, true)
		if err != nil {
			return nil, err
		}
		name, err = directory(name, !mustExist)
		if err != nil {
			return nil, err
		}
		for _, previous := range out {
			same, err := samePath(previous, name)
			if err != nil || same {
				return nil, fmt.Errorf("duplicate or unverifiable path %s: %v", name, err)
			}
		}
		out = append(out, name)
	}
	slices.Sort(out)
	return out, nil
}

func samePath(a, b string) (bool, error) {
	ab, err := paths.Inside(a, b)
	if err != nil || !ab {
		return false, err
	}
	return paths.Inside(b, a)
}

func storagePaths(ctx context.Context, repo, base string, p Proposal, plan *Plan, runner execx.Runner) error {
	roots, err := pathList(base, []string{p.StorageRoot}, true)
	if err != nil {
		return fmt.Errorf("storage_root: %w", err)
	}
	plan.StorageRoot = roots[0]
	if len(p.WorkerRoots) == 0 {
		return fmt.Errorf("worker_roots must declare at least one worker location")
	}
	plan.WorkerRoots, err = pathList(base, p.WorkerRoots, false)
	if err != nil {
		return fmt.Errorf("worker_roots: %w", err)
	}
	plan.ScratchRoots, err = pathList(base, p.ScratchRoots, false)
	if err != nil {
		return fmt.Errorf("scratch_roots: %w", err)
	}
	meta, err := gitRead(ctx, runner, repo, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir", "--git-dir")
	if err != nil {
		return fmt.Errorf("verify repository/Git storage exclusions: %w", err)
	}
	fields := strings.Split(strings.TrimRight(meta, "\r\n"), "\n")
	if len(fields) != 3 {
		return fmt.Errorf("unverifiable repository/Git directory metadata")
	}
	for i := range fields {
		fields[i] = strings.TrimSuffix(fields[i], "\r")
	}
	same, err := samePath(repo, fields[0])
	if err != nil || !same {
		return fmt.Errorf("invocation must be at Git repository root; metadata mismatch: %v", err)
	}
	gitDirs := []string{fields[1]}
	if same, err := samePath(fields[1], fields[2]); err != nil {
		return err
	} else if !same {
		gitDirs = append(gitDirs, fields[2])
	}
	plan.GitDirectories, err = pathList(base, gitDirs, true)
	if err != nil {
		return fmt.Errorf("git directories: %w", err)
	}
	worktrees, err := gitRead(ctx, runner, repo, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return err
	}
	names := []string{}
	for _, field := range strings.Split(worktrees, "\x00") {
		if strings.HasPrefix(field, "worktree ") {
			names = append(names, strings.TrimPrefix(field, "worktree "))
		}
	}
	if len(names) == 0 {
		return fmt.Errorf("git worktree paths unavailable; cannot verify storage exclusions")
	}
	// With a separate Git directory, worktree list may describe its parent
	// instead of the current checkout. rev-parse verified the current root.
	if !slices.ContainsFunc(names, func(root string) bool { same, err := samePath(repo, root); return err == nil && same }) {
		names = append(names, fields[0])
	}
	plan.RepositoryRoots, err = pathList(base, names, true)
	if err != nil {
		return fmt.Errorf("repository roots: %w", err)
	}
	for _, excluded := range append(append(slices.Clone(plan.RepositoryRoots), plan.GitDirectories...), append(slices.Clone(plan.WorkerRoots), plan.ScratchRoots...)...) {
		for _, pair := range [][2]string{{excluded, plan.StorageRoot}, {plan.StorageRoot, excluded}} {
			overlaps, err := paths.Inside(pair[0], pair[1])
			if err != nil || overlaps {
				return fmt.Errorf("storage_root overlaps or cannot be distinguished from excluded path %s: %v", excluded, err)
			}
		}
	}
	return nil
}

// save uses one complete file, atomically linked under its digest. Link never
// replaces an existing target; no partial .pending file is a completed record.
// Modes are ordinary local hygiene, not proof of worker containment.
func save(record *Record) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > maxRecordBytes {
		return fmt.Errorf("normalized record exceeds %d-byte storage limit", maxRecordBytes)
	}
	if _, err := directory(record.Plan.StorageRoot, false); err != nil {
		return err
	}
	root, err := openDirectory(record.Plan.StorageRoot)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	name := strings.TrimPrefix(record.PlanDigest, "sha256:") + ".json"
	if !digestPattern.MatchString(strings.TrimSuffix(name, ".json")) {
		return fmt.Errorf("invalid normalized plan digest")
	}
	if _, err := root.Lstat(name); err == nil {
		return existingRecord(root, name, data)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	temp := ".pending-" + rand.Text()
	f, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(temp) }()
	_, writeErr := f.Write(data)
	err = errors.Join(writeErr, f.Sync(), f.Close())
	if err != nil {
		return fmt.Errorf("retain pending preview: %w", err)
	}
	if err := root.Link(temp, name); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return existingRecord(root, name, data)
		}
		return fmt.Errorf("atomic no-replace preview publication unavailable: %w", err)
	}
	if err := root.Remove(temp); err != nil {
		return fmt.Errorf("complete record retained but pending link cleanup failed: %w", err)
	}
	return existingRecord(root, name, data)
}

func existingRecord(root *os.Root, name string, expected []byte) error {
	// Another publisher may be between Link and removing its pending name.
	// Wait only for that transient alias; persistent hard links fail closed.
	var data []byte
	var err error
	for attempt := 0; attempt < 50; attempt++ {
		data, err = readRoot(root, name, maxRecordBytes)
		if !errors.Is(err, errLinkedFile) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		return fmt.Errorf("existing preview record unverifiable; preserve it for maintainer inspection: %w", err)
	}
	if !bytes.Equal(data, expected) {
		return fmt.Errorf("existing preview record conflicts or is corrupt; preserved without replacement: %s", name)
	}
	return nil
}
