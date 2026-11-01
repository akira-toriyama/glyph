package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// NotAFileShapes are six of the shapes other than a regular file a glyph.toml
// path can hold — the ones the tests stage (a socket and a device are not
// among them). Each row's Stage creates, at the path it is given, a
// directory, a named pipe, or a symbolic link that resolves to no file — one
// to nothing, one to itself, one through a regular file it writes beside the
// path (shared.toml), one to a name past NAME_MAX (255). Shape names the row;
// Kind is the phrase config.NotAFileError names what is there by.
//
// config's own tests keep a copy of these rows that also carries the file mode
// each one stats as: config's internal tests cannot import testutil, which
// imports config.
var NotAFileShapes = []struct {
	Shape, Kind string
	Stage       func(path string) error
}{
	{"a directory", "a directory", func(p string) error { return os.Mkdir(p, 0o700) }},
	{"a named pipe", "a named pipe", func(p string) error { return syscall.Mkfifo(p, 0o600) }},
	{"a link to nothing", "a broken symbolic link", func(p string) error { return os.Symlink("nowhere.toml", p) }},
	{"a link to itself", "a broken symbolic link", func(p string) error { return os.Symlink(filepath.Base(p), p) }},
	{"a link through a file", "a broken symbolic link", func(p string) error {
		if err := os.WriteFile(filepath.Join(filepath.Dir(p), "shared.toml"), nil, 0o600); err != nil {
			return err
		}
		return os.Symlink("shared.toml/glyph.toml", p)
	}},
	{"a link to an over-long name", "a broken symbolic link", func(p string) error { return os.Symlink(strings.Repeat("a", 300), p) }},
}
