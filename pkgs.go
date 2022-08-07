package pkgs

import (
	"fmt"
	"go/build"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/charlievieth/buildutil"
	"github.com/charlievieth/fastwalk"
)

// TODO:
// 	1. support modules with golang.org/x/mod/modfile
// 	2. parse the repos vendor/modules separately the
// 	   any vendor/modules dir/file encountered.

type Pkg struct {
	Name       string // package name
	ImportPath string // pkg import path ("net/http", "foo/bar/vendor/a/b")
}

type walker struct {
	importDir string
	srcDir    string
	pkgDir    string
	ctxt      *build.Context
	pkgs      map[string]*Pkg // abs dir path => *pkg
	mu        sync.RWMutex
	// ignored directories (full path)
	ignored map[string]struct{}
}

func isDir(ctxt *build.Context, name string) bool {
	if name == "" {
		return false
	}
	if ctxt.IsDir != nil {
		return ctxt.IsDir(name)
	}
	fi, err := os.Stat(name)
	return err == nil && fi.IsDir()
}

func newWalker(importDir, srcDir string, ctxt *build.Context) (*walker, error) {
	var pkgtargetroot string
	switch ctxt.Compiler {
	case "gccgo":
		pkgtargetroot = "pkg/gccgo_" + ctxt.GOOS + "_" + ctxt.GOARCH
	case "gc":
		pkgtargetroot = "pkg/" + ctxt.GOOS + "_" + ctxt.GOARCH
	default:
		return nil, fmt.Errorf("pkgs: unknown compiler %q", ctxt.Compiler)
	}
	if ctxt.InstallSuffix != "" {
		pkgtargetroot += "_" + ctxt.InstallSuffix
	}
	pkgDir := srcDir[:len(srcDir)-len("src")] + pkgtargetroot
	if importDir != "" {
		importDir = filepath.Clean(importDir)
	}

	// special vgo directories
	ignored := map[string]struct{}{
		filepath.Join(srcDir, "v"):   {},
		filepath.Join(srcDir, "mod"): {},
		filepath.Join(pkgDir, "v"):   {},
		filepath.Join(pkgDir, "mod"): {},
	}
	w := &walker{
		importDir: importDir,
		srcDir:    toSlash(srcDir),
		pkgDir:    toSlash(pkgDir),
		ctxt:      ctxt,
		pkgs:      make(map[string]*Pkg),
		ignored:   ignored,
	}
	return w, nil
}

func (w *walker) Update() error {
	// TODO: Add 'AllowBinary' mode so that pkgs are not
	// included if the source code has been deleted.
	if isDir(w.ctxt, w.pkgDir) && !strings.HasPrefix(w.srcDir, w.ctxt.GOROOT) {
		if err := fastwalk.Walk(nil, w.pkgDir, w.walkPkg); err != nil {
			return err
		}
	}
	conf := fastwalk.Config{
		Follow: true,
	}
	if err := fastwalk.Walk(&conf, w.srcDir, w.walk); err != nil {
		return err
	}
	return nil
}

func (w *walker) seen(dirname string) (ok bool) {
	w.mu.RLock()
	if w.pkgs != nil {
		_, ok = w.pkgs[dirname]
	}
	w.mu.RUnlock()
	return
}

func skipDir(importDir, path, base string) bool {
	if base != "vendor" && base != "internal" {
		return false
	}
	dir := toSlash(filepath.Dir(path))
	if strings.HasSuffix(dir, "/") {
		dir = strings.TrimLeft(dir, "/")
	}
	return importDir == "" || !strings.HasPrefix(importDir, dir)
}

// func(path string, d DirEntry, err error) error
func (w *walker) walkPkg(path string, d fs.DirEntry, err error) error {
	if err != nil {
		return err
	}
	typ := d.Type()
	if typ.IsRegular() {
		if !strings.HasSuffix(path, ".a") {
			return nil
		}
		// $GOPATH/pkg/GOOS_GOARCH/pkgname... => $GOPATH/src/pkgname...
		dir := strings.TrimSuffix(w.srcDir+path[len(w.pkgDir):], ".a")
		if w.seen(dir) {
			return nil
		}
		w.mu.Lock()
		if w.pkgs == nil {
			w.pkgs = make(map[string]*Pkg)
		}
		if _, dup := w.pkgs[dir]; !dup {
			importpath := vendorlessImportPath(toSlash(dir[len(w.srcDir)+len("/"):]))
			w.pkgs[dir] = &Pkg{
				Name:       filepath.Base(dir), // don't use path - must trim '.a'
				ImportPath: importpath,
			}
		}
		w.mu.Unlock()
		return nil
	}
	if typ == os.ModeDir {
		name := d.Name()
		if name == "" || name[0] == '.' || name[0] == '_' ||
			name == "testdata" || name == "node_modules" {
			return filepath.SkipDir
		}
		if name == "v" || name == "mod" {
			if _, ok := w.ignored[path]; ok {
				return filepath.SkipDir
			}
		}
		if skipDir(w.importDir, path, name) {
			return filepath.SkipDir
		}
		return nil
	}
	return nil
}

func toSlash(path string) string {
	if filepath.Separator == '/' {
		return path
	}
	if n := strings.IndexByte(path, filepath.Separator); n == -1 {
		return path
	}
	buf := make([]byte, len(path))
	for i := 0; i < len(path); i++ {
		c := path[i]
		if c != filepath.Separator {
			buf[i] = c
		} else {
			buf[i] = '/'
		}
	}
	return string(buf)
}

func (w *walker) walk(path string, d fs.DirEntry, err error) error {
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	typ := d.Type()
	if typ.IsRegular() {
		if dir == w.srcDir || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if w.seen(dir) {
			return fastwalk.ErrSkipFiles
		}
		name, ok := buildutil.ShortImport(w.ctxt, path)
		if !ok {
			return nil
		}
		w.mu.Lock()
		if _, dup := w.pkgs[dir]; !dup {
			importpath := vendorlessImportPath(toSlash(dir[len(w.srcDir)+len("/"):]))
			w.pkgs[dir] = &Pkg{
				Name:       name,
				ImportPath: importpath,
			}
			w.mu.Unlock()
			return fastwalk.ErrSkipFiles
		}
		w.mu.Unlock()
		return nil
	}
	if typ == os.ModeDir {
		base := filepath.Base(path)
		if base == "" || base[0] == '.' || base[0] == '_' ||
			base == "testdata" || base == "node_modules" {
			return filepath.SkipDir
		}
		// Ignore the "builtin" package
		if base == "builtin" {
			return filepath.SkipDir
		}
		if base == "v" || base == "mod" {
			if _, ok := w.ignored[path]; ok {
				return filepath.SkipDir
			}
		}
		if skipDir(w.importDir, path, base) {
			return filepath.SkipDir
		}
		return nil
	}
	return nil
}

// pre-computed Rabin-Karp search for "/vendor/" based on strings.LastIndex
func lastVendor(s string) int {
	const (
		primeRK = 16777619
		hashss  = 1776373440
		pow     = 1566662433
		substr  = "/vendor/"
		n       = len(substr)
	)
	if n == len(s) {
		if substr == s {
			return 0
		}
		return -1
	}
	if n > len(s) {
		return -1
	}
	last := len(s) - n
	var h uint32
	for i := len(s) - 1; i >= last; i-- {
		h = h*primeRK + uint32(s[i])
	}
	if h == hashss && s[last:] == substr {
		return last
	}
	for i := last - 1; i >= 0; i-- {
		h *= primeRK
		h += uint32(s[i])
		h -= pow * uint32(s[i+n])
		if h == hashss && s[i:i+n] == substr {
			return i
		}
	}
	return -1
}

// vendorlessImportPath returns the devendorized version of the provided import path.
// e.g. "foo/bar/vendor/a/b" => "a/b"
func vendorlessImportPath(ipath string) string {
	// Devendorize for use in import statement.
	if i := lastVendor(ipath); i >= 0 {
		return ipath[i+len("/vendor/"):]
	}
	if strings.HasPrefix(ipath, "vendor/") {
		return ipath[len("vendor/"):]
	}
	return ipath
}

func Walk(ctxt *build.Context, importDir string) ([]string, error) {
	var first error
	var paths []string
	srcDirs := ctxt.SrcDirs()
	for _, s := range srcDirs {
		w, err := newWalker(importDir, s, ctxt)
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		if err := w.Update(); err != nil {
			if first == nil {
				first = err
			}
		}
		if paths == nil {
			paths = make([]string, 0, len(w.pkgs))
		}
		for _, p := range w.pkgs {
			if p.Name != "main" {
				paths = append(paths, p.ImportPath)
			}
		}
	}

	// TODO: sort paths
	return paths, first
}
