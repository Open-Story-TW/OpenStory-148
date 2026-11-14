package scripts

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// library is the embedded sources with an optional directory laid over them.
// A file in the directory replaces the embedded file of the same name or adds
// a new one; the directory is only read by Prepare, so a half-saved file never
// reaches a running server.
type library struct {
	mu   sync.RWMutex
	disk map[string][]byte
}

var sources = &library{}

// scriptDirs are the folders a source may live in, as in the embed pattern.
var scriptDirs = []string{"npc", "quest", "portal", "map/on_user_enter"}

var embeddedNames = sync.OnceValue(func() []string {
	var names []string
	_ = fs.WalkDir(embedded, ".", func(name string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			names = append(names, name)
		}
		return err
	})
	return names
})

// ReadFile is a source exactly as stored, without the appended driver.
func (l *library) ReadFile(name string) ([]byte, error) {
	l.mu.RLock()
	data, ok := l.disk[name]
	l.mu.RUnlock()
	if ok {
		return data, nil
	}
	return embedded.ReadFile(name)
}

func (l *library) source(name string) ([]byte, error) {
	l.mu.RLock()
	disk := l.disk
	l.mu.RUnlock()
	data, ok := effective(disk, name)
	if !ok {
		return nil, fs.ErrNotExist
	}
	return data, nil
}

func (l *library) names() []string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return allNames(l.disk)
}

func isDriver(name string) bool { return strings.HasPrefix(name, "legacy/") }

// allNames lists every source in the embedded set and in disk, sorted.
func allNames(disk map[string][]byte) []string {
	names := append([]string(nil), embeddedNames()...)
	for name := range disk {
		if _, err := embedded.ReadFile(name); err != nil && !isDriver(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func rawFile(disk map[string][]byte, name string) ([]byte, bool) {
	if data, ok := disk[name]; ok {
		return data, true
	}
	if data, ok := embeddedDrivers()[name]; ok {
		return data, true
	}
	data, err := embedded.ReadFile(name)
	return data, err == nil
}

// effective is the source the engine runs: the file plus its shared driver.
func effective(disk map[string][]byte, name string) ([]byte, bool) {
	data, ok := rawFile(disk, name)
	if !ok || isDriver(name) {
		return nil, false
	}
	file := driverFile(name, data)
	if file == "" {
		return data, true
	}
	driver, _ := rawFile(disk, file)
	return append(append(make([]byte, 0, len(data)+len(driver)+1), data...), append([]byte("\n"), driver...)...), true
}

// Update is a directory read that has been checked but not yet applied.
type Update struct {
	// Changed holds the new full source of every added or edited script.
	Changed map[string][]byte
	// Removed lists scripts that no longer exist in the directory or binary.
	Removed []string
	// Rejected maps a file that failed its check to the reason. Such a file
	// keeps the version the server was already running.
	Rejected map[string]string
	disk     map[string][]byte
}

// Prepare reads dir, which is laid out like this package (npc/, quest/,
// portal/, map/on_user_enter/ and legacy/*_driver.lua), and compares it with
// what is loaded now. check must report whether the engine accepts a source.
// An empty dir means the embedded sources alone. Nothing changes until Commit.
func Prepare(dir string, check func(name string, source []byte) error) (*Update, error) {
	next := map[string][]byte{}
	if dir != "" {
		if info, err := os.Stat(dir); err != nil {
			return nil, err
		} else if !info.IsDir() {
			return nil, errors.New("script directory is not a directory")
		}
		for _, sub := range append([]string{"legacy"}, scriptDirs...) {
			entries, err := os.ReadDir(filepath.Join(dir, filepath.FromSlash(sub)))
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			for _, entry := range entries {
				name := sub + "/" + entry.Name()
				if entry.IsDir() || !strings.HasSuffix(name, ".lua") {
					continue
				}
				if _, known := embeddedDrivers()[name]; sub == "legacy" && !known {
					continue
				}
				data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
				if err != nil {
					return nil, err
				}
				// Git on Windows may check sources out with CRLF.
				next[name] = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
			}
		}
	}
	sources.mu.RLock()
	old := sources.disk
	sources.mu.RUnlock()
	u := &Update{Changed: map[string][]byte{}, Rejected: map[string]string{}, disk: next}
	restore := func(name string) {
		if data, ok := old[name]; ok {
			next[name] = data
		} else {
			delete(next, name)
		}
	}
	// A driver is shared: try it with the sources already running, and keep
	// the old driver if it breaks any of them.
	for file := range embeddedDrivers() {
		was, _ := rawFile(old, file)
		now, _ := rawFile(next, file)
		if bytes.Equal(was, now) {
			continue
		}
		trial := map[string][]byte{file: now}
		for name, data := range old {
			if name != file {
				trial[name] = data
			}
		}
		for _, name := range allNames(old) {
			if data, _ := rawFile(old, name); driverFile(name, data) != file {
				continue
			}
			full, _ := effective(trial, name)
			if err := check(name, full); err != nil {
				u.Rejected[file] = name + ": " + err.Error()
				restore(file)
				break
			}
		}
	}
	seen := map[string]bool{}
	for _, name := range append(allNames(old), allNames(next)...) {
		if seen[name] {
			continue
		}
		seen[name] = true
		was, existed := effective(old, name)
		now, ok := effective(next, name)
		if ok && !bytes.Equal(was, now) {
			if err := check(name, now); err != nil {
				u.Rejected[name] = err.Error()
				restore(name)
				now, ok = effective(next, name)
			}
		}
		switch {
		case !ok:
			// A new file that was rejected never existed: nothing to remove.
			if existed {
				u.Removed = append(u.Removed, name)
			}
		case !bytes.Equal(was, now):
			u.Changed[name] = now
		}
	}
	return u, nil
}

// Commit makes the prepared directory read the one Source answers from.
func (u *Update) Commit() {
	sources.mu.Lock()
	sources.disk = u.disk
	sources.mu.Unlock()
}
