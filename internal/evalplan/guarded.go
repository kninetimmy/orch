package evalplan

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// guardedDir uses the very same path, reparse, drive and link checks as preview.
// Each handle also retains its directory identity; renaming/replacing a path
// cannot turn a retained handle into authority to write a different directory.
type guardedDir struct {
	path   string
	root   *os.Root
	info   os.FileInfo
	budget *writeBudget
	depth  int
}

// One controller owns writes after its exclusive start claim. Children share
// the monotonic byte ceiling; failed/pending writes never refund capacity.
type writeBudget struct {
	mu      sync.Mutex
	used    int64
	entries int
}

func (g *guardedDir) reserve(size, entries int) error {
	if g.budget == nil {
		return nil
	}
	g.budget.mu.Lock()
	defer g.budget.mu.Unlock()
	if int64(size) > maxControllerBytes-g.budget.used || entries > maxControllerEntries-g.budget.entries {
		return fmt.Errorf("controller evidence exceeds %d-byte capacity", maxControllerBytes)
	}
	g.budget.used += int64(size)
	g.budget.entries += entries
	return nil
}

func openGuarded(name string) (*guardedDir, error) {
	name, err := localPath("", name, true)
	if err != nil {
		return nil, err
	}
	name, err = directory(name, false)
	if err != nil {
		return nil, err
	}
	root, err := openDirectory(name)
	if err != nil {
		return nil, err
	}
	info, err := root.Stat(".")
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	g := &guardedDir{path: name, root: root, info: info}
	if err := g.check(); err != nil {
		g.close()
		return nil, err
	}
	return g, nil
}

func (g *guardedDir) close() { _ = g.root.Close() }

func (g *guardedDir) check() error {
	if _, err := directory(g.path, false); err != nil {
		return err
	}
	info, err := os.Lstat(g.path)
	if err != nil || !os.SameFile(g.info, info) {
		return fmt.Errorf("controller directory identity changed: %s: %v", g.path, err)
	}
	return nil
}

func relativeName(name string) error {
	if !fs.ValidPath(name) || name == "." || strings.Contains(name, `\`) || filepath.IsAbs(name) {
		return fmt.Errorf("require a portable relative artifact name: %q", name)
	}
	_, err := localPath("", name, false)
	return err
}

// child opens one component at a time, checking identities before following it.
func (g *guardedDir) child(name string) (*guardedDir, error) {
	if err := relativeName(name); err != nil {
		return nil, err
	}
	if err := g.check(); err != nil {
		return nil, err
	}
	if g.depth+len(strings.Split(name, "/")) > 32 {
		return nil, fmt.Errorf("controller directory depth exceeds 32")
	}
	current := g
	for _, part := range strings.Split(name, "/") {
		info, err := current.root.Lstat(part)
		if err != nil || !info.IsDir() || reparse(info) {
			if current != g {
				current.close()
			}
			if err != nil {
				return nil, fmt.Errorf("inspect child directory %s: %w", name, err)
			}
			return nil, fmt.Errorf("linked or unverifiable child directory %s", name)
		}
		root, err := current.root.OpenRoot(part)
		childPath := filepath.Join(current.path, part)
		if current != g {
			current.close()
		}
		if err != nil {
			return nil, err
		}
		opened, err := root.Stat(".")
		if err != nil || !os.SameFile(info, opened) {
			_ = root.Close()
			return nil, fmt.Errorf("child directory identity changed: %s: %v", name, err)
		}
		current = &guardedDir{path: childPath, root: root, info: opened, budget: g.budget, depth: current.depth + 1}
	}
	if err := current.check(); err != nil {
		current.close()
		return nil, err
	}
	return current, nil
}

// createDir is deliberately exclusive. Existing/partial resources are preserved.
func (g *guardedDir) createDir(name string) (*guardedDir, error) {
	if err := relativeName(name); err != nil || strings.Contains(name, "/") {
		return nil, fmt.Errorf("require one new directory component: %q", name)
	}
	if err := g.check(); err != nil {
		return nil, err
	}
	if g.depth >= 32 {
		return nil, fmt.Errorf("controller directory depth exceeds 32")
	}
	if err := g.reserve(0, 1); err != nil {
		return nil, err
	}
	if err := g.root.Mkdir(name, 0o700); err != nil {
		return nil, fmt.Errorf("create exclusive controller resource %s: %w", name, err)
	}
	return g.child(name)
}

func (g *guardedDir) parent(name string, create bool) (*guardedDir, string, error) {
	if err := relativeName(name); err != nil {
		return nil, "", err
	}
	parts := strings.Split(name, "/")
	if g.depth+len(parts)-1 > 32 {
		return nil, "", fmt.Errorf("controller artifact depth exceeds 32")
	}
	current := g
	for _, part := range parts[:len(parts)-1] {
		if err := current.check(); err != nil {
			if current != g {
				current.close()
			}
			return nil, "", err
		}
		if create {
			if _, err := current.root.Lstat(part); errors.Is(err, fs.ErrNotExist) {
				if err := current.reserve(0, 1); err != nil {
					if current != g {
						current.close()
					}
					return nil, "", err
				}
			} else if err != nil {
				if current != g {
					current.close()
				}
				return nil, "", err
			}
			if err := current.root.Mkdir(part, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
				if current != g {
					current.close()
				}
				return nil, "", err
			}
		}
		child, err := current.child(part)
		if current != g {
			current.close()
		}
		if err != nil {
			return nil, "", err
		}
		current = child
	}
	return current, parts[len(parts)-1], nil
}

func (g *guardedDir) read(name string, limit int) ([]byte, error) {
	p, base, err := g.parent(name, false)
	if err != nil {
		return nil, err
	}
	if p != g {
		defer p.close()
	}
	if err := p.check(); err != nil {
		return nil, err
	}
	var data []byte
	for attempt := 0; attempt < 50; attempt++ {
		data, err = readRoot(p.root, base, limit)
		if !strings.HasSuffix(base, ".json") || !errors.Is(err, errLinkedFile) {
			break
		}
		// A completed immutable record can briefly have its publisher's pending
		// link. Persistent aliases still fail closed after the same bounded grace
		// used by preview publication; concurrent stop readers see no partial JSON.
		time.Sleep(20 * time.Millisecond)
	}
	return data, errors.Join(err, p.check())
}

func (g *guardedDir) publish(name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > maxRecordBytes {
		return fmt.Errorf("controller record exceeds %d-byte limit", maxRecordBytes)
	}
	if err := relativeName(name); err != nil || strings.Contains(name, "/") {
		return fmt.Errorf("require one record filename: %q", name)
	}
	if err := g.check(); err != nil {
		return err
	}
	if _, err := g.root.Lstat(name); err == nil {
		return errors.Join(existingRecord(g.root, name, data), g.check())
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := g.reserve(len(data), 2); err != nil { // charge pending and published names conservatively
		return err
	}
	return errors.Join(publishRoot(g.root, name, data), g.check())
}

func (g *guardedDir) writeFile(name string, data []byte) error {
	if len(data) > MaxArtifactBytes {
		return fmt.Errorf("artifact %s exceeds %d-byte output limit", name, MaxArtifactBytes)
	}
	if err := g.reserve(len(data), 1); err != nil {
		return err
	}
	p, base, err := g.parent(name, true)
	if err != nil {
		return err
	}
	if p != g {
		defer p.close()
	}
	if err := p.check(); err != nil {
		return err
	}
	f, err := p.root.OpenFile(base, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	return errors.Join(writeErr, f.Sync(), f.Close(), p.check())
}
