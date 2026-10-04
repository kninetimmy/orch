package evalcorpus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const maxFileBytes = 2 * 1024 * 1024

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > maxFileBytes-b.Len() {
		return 0, fmt.Errorf("historical Git output exceeds %d-byte preparation limit", maxFileBytes)
	}
	return b.Buffer.Write(p)
}

// gitRead never invokes a shell and never fetches missing history.
func gitRead(ctx context.Context, repo string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-optional-locks", "--no-pager", "-c", "core.fsmonitor=false"}, args...)...)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_NO_REPLACE_OBJECTS=1", "GIT_NO_LAZY_FETCH=1", "LC_ALL=C")
	cmd.WaitDelay = 2 * time.Second
	var output, diagnostic boundedOutput
	cmd.Stdout, cmd.Stderr = &output, &diagnostic
	err := cmd.Run()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, fmt.Errorf("git %s: %w", args[0], err)
		}
		return nil, fmt.Errorf("git %s: %w", args[0], err)
	}
	return output.Bytes(), nil
}

// ReadHistoricalFile reuses the reference export's fixed Git/tree checks for a
// single digested historical blob. Local authored artifacts instead use the
// controller's anchored reader. It never fetches or executes artifact contents.
func ReadHistoricalFile(ctx context.Context, repo string, f File) ([]byte, error) {
	if err := validateFiles([]File{f}, false); err != nil || f.Commit == "" {
		return nil, fmt.Errorf("require a valid pinned historical file: %v", err)
	}
	data, err := historicalFile(ctx, repo, f)
	if err != nil {
		return nil, err
	}
	if Digest(data) != f.SHA256 {
		return nil, fmt.Errorf("digest mismatch for %s", f.Source)
	}
	return data, nil
}

func historicalFile(ctx context.Context, repo string, f File) ([]byte, error) {
	if _, err := gitRead(ctx, repo, "cat-file", "-e", f.Commit+"^{commit}"); err != nil {
		return nil, fmt.Errorf("missing source commit %s: %w", f.Commit, err)
	}
	parts := strings.Split(f.Source, "/")
	var oid string
	for i := range parts {
		p := strings.Join(parts[:i+1], "/")
		data, err := gitRead(ctx, repo, "ls-tree", "-z", f.Commit, "--", p)
		if err != nil {
			return nil, err
		}
		record := strings.TrimSuffix(string(data), "\x00")
		meta, name, ok := strings.Cut(record, "\t")
		fields := strings.Fields(meta)
		if !ok || name != p || len(fields) != 3 {
			return nil, fmt.Errorf("missing source path %s at %s", p, f.Commit)
		}
		switch fields[0] {
		case "120000":
			return nil, fmt.Errorf("source link forbidden: %s at %s", p, f.Commit)
		case "160000":
			return nil, fmt.Errorf("source submodule forbidden: %s at %s", p, f.Commit)
		}
		if i < len(parts)-1 && fields[1] != "tree" {
			return nil, fmt.Errorf("source ancestor is not a tree: %s", p)
		}
		if i == len(parts)-1 && (!slices.Contains([]string{"100644", "100755"}, fields[0]) || fields[1] != "blob") {
			return nil, fmt.Errorf("source is not a regular blob: %s", p)
		}
		oid = fields[2]
	}
	return gitRead(ctx, repo, "cat-file", "blob", oid)
}

func localFile(root *os.Root, p string) ([]byte, error) {
	parts := strings.Split(p, "/")
	for i := range parts {
		name := filepath.FromSlash(strings.Join(parts[:i+1], "/"))
		info, err := root.Lstat(name)
		if err != nil {
			return nil, fmt.Errorf("corpus artifact %s: %w", p, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 || (i < len(parts)-1 && !info.IsDir()) || (i == len(parts)-1 && !info.Mode().IsRegular()) {
			return nil, fmt.Errorf("corpus link/nonregular artifact forbidden: %s", name)
		}
	}
	return root.ReadFile(filepath.FromSlash(p))
}

func readFiles(ctx context.Context, repo, corpusDir string, files []File, worker bool) (map[string][]byte, error) {
	if err := validateFiles(files, worker); err != nil {
		return nil, err
	}
	if err := directoryWithoutLinks(corpusDir); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(corpusDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	data := make(map[string][]byte, len(files))
	for _, f := range files {
		var b []byte
		if f.Commit != "" {
			b, err = historicalFile(ctx, repo, f)
		} else {
			b, err = localFile(root, f.Source)
		}
		if err != nil {
			return nil, err
		}
		if len(b) > maxFileBytes {
			return nil, fmt.Errorf("file %s exceeds %d-byte preparation limit", f.Source, maxFileBytes)
		}
		if Digest(b) != f.SHA256 {
			return nil, fmt.Errorf("digest mismatch for %s (want %s, observed %s)", f.Source, f.SHA256, Digest(b))
		}
		data[f.Path] = b
	}
	return data, nil
}

func directoryWithoutLinks(dir string) error {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	for p := dir; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil || !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("require existing directory without links: %s: %v", p, err)
		}
		if filepath.Dir(p) == p {
			return nil
		}
	}
}

// Export creates name under an existing disposable parent, after verifying all
// declared worker bytes. It refuses existing destinations and parent links.
// Controller material is not implicitly copied. os.Root confines file creation;
// this is packet preparation, not runtime isolation against a worker or model.
func Export(ctx context.Context, repo, corpusDir, parent, name string, files []File) (string, error) {
	if err := safePath(name, true); err != nil || strings.Contains(name, "/") {
		return "", fmt.Errorf("invalid destination name %q", name)
	}
	parent, err := filepath.Abs(parent)
	if err != nil {
		return "", err
	}
	if err := directoryWithoutLinks(parent); err != nil {
		return "", err
	}
	parentRoot, err := os.OpenRoot(parent)
	if err != nil {
		return "", err
	}
	defer func() { _ = parentRoot.Close() }()
	if _, err := parentRoot.Lstat(name); err == nil {
		return "", fmt.Errorf("destination already exists: %s", filepath.Join(parent, name))
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	data, err := readFiles(ctx, repo, corpusDir, files, true)
	if err != nil {
		return "", err
	}
	if err := parentRoot.Mkdir(name, 0o700); err != nil {
		return "", fmt.Errorf("create new destination: %w", err)
	}
	root, err := parentRoot.OpenRoot(name)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	for _, f := range files {
		p := filepath.FromSlash(f.Path)
		if err := root.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return "", err
		}
		file, err := root.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return "", err
		}
		_, writeErr := file.Write(data[f.Path])
		if err := errors.Join(writeErr, file.Close()); err != nil {
			return "", err
		}
	}
	if err := Inspect(root, files); err != nil {
		return "", err
	}
	return filepath.Join(parent, name), nil
}

// Inspect checks exact contents and bytes, including rejecting extra files and
// links. It cannot prove OS permissions, Git-store denial, or model-tool closure.
func Inspect(root *os.Root, files []File) error {
	want := map[string]string{}
	for _, f := range files {
		want[f.Path] = f.SHA256
	}
	err := fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("packet link forbidden: %s", p)
		}
		if d.IsDir() {
			if p != "." && !slices.ContainsFunc(files, func(f File) bool { return strings.HasPrefix(f.Path, p+"/") }) {
				return fmt.Errorf("undeclared packet directory: %s", p)
			}
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("packet nonregular file forbidden: %s: %v", p, err)
		}
		b, err := root.ReadFile(filepath.FromSlash(p))
		if err != nil {
			return err
		}
		if want[p] == "" || Digest(b) != want[p] {
			return fmt.Errorf("unexpected packet file or digest: %s", p)
		}
		delete(want, p)
		return nil
	})
	if err != nil {
		return err
	}
	if len(want) != 0 {
		return fmt.Errorf("packet missing %d declared files", len(want))
	}
	return nil
}
