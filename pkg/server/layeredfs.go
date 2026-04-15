package server

import "io/fs"

// layeredFS implements fs.FS by checking primary first, falling back to
// secondary. Used to serve fingerprinted assets from dist/ with a fallback
// to the source static/ directory.
type layeredFS struct {
	primary   fs.FS
	secondary fs.FS
}

func newLayeredFS(primary, secondary fs.FS) fs.FS {
	return &layeredFS{primary: primary, secondary: secondary}
}

func (l *layeredFS) Open(name string) (fs.File, error) {
	f, err := l.primary.Open(name)
	if err == nil {
		return f, nil
	}
	return l.secondary.Open(name)
}
