package pkgs

import (
	"fmt"
	"go/build"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/charlievieth/buildutil"
)

type Pkg struct {
	Name       string // package name
	ImportPath string // full pkg import path ("net/http", "foo/bar/vendor/a/b")
}

type Walker struct {
	importDir string
	srcDir    string
	pkgDir    string
	ctxt      *build.Context
	pkgs      map[string]*Pkg // abs dir path => *pkg
	mu        sync.RWMutex
}

func newWalker(importDir, srcDir string, ctxt *build.Context) (*Walker, error) {
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
	w := &Walker{
		importDir: importDir,
		srcDir:    filepath.ToSlash(srcDir),
		pkgDir:    filepath.ToSlash(pkgDir),
		ctxt:      ctxt,
		pkgs:      make(map[string]*Pkg),
	}
	return w, nil
}

func (w *Walker) Update() error {
	// TODO: Add 'AllowBinary' mode so that pkgs are not
	// included if the source code has been deleted.
	if w.pkgDir != "" && !strings.HasPrefix(w.srcDir, w.ctxt.GOROOT) {
		if err := fastWalk(w.pkgDir, w.walkPkg); err != nil {
			return err
		}
	}
	if err := fastWalk(w.srcDir, w.walk); err != nil {
		return err
	}
	return nil
}

func (w *Walker) seen(dirname string) (ok bool) {
	w.mu.RLock()
	if w.pkgs != nil {
		_, ok = w.pkgs[dirname]
	}
	w.mu.RUnlock()
	return
}

func (w *Walker) skipDir(path, base string) bool {
	return w.importDir != "" && (base == "vendor" || base == "internal") &&
		!strings.HasPrefix(path, w.importDir)
}

func (w *Walker) walkPkg(path string, typ os.FileMode) error {
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
			importpath := vendorlessImportPath(filepath.ToSlash(dir[len(w.srcDir)+len("/"):]))
			w.pkgs[dir] = &Pkg{
				Name:       filepath.Base(dir), // don't use path - must trim '.a'
				ImportPath: importpath,
			}
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
		if w.skipDir(path, base) {
			return filepath.SkipDir
		}
		return nil
	}
	return nil
}

func (w *Walker) walk(path string, typ os.FileMode) error {
	dir := filepath.Dir(path)
	if typ.IsRegular() {
		if dir == w.srcDir || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if w.seen(dir) {
			return nil
		}
		name, ok := buildutil.ShortImport(w.ctxt, path)
		if !ok {
			return nil
		}
		w.mu.Lock()
		if w.pkgs == nil {
			w.pkgs = make(map[string]*Pkg)
		}
		if _, dup := w.pkgs[dir]; !dup {
			importpath := vendorlessImportPath(filepath.ToSlash(dir[len(w.srcDir)+len("/"):]))
			w.pkgs[dir] = &Pkg{
				Name:       name,
				ImportPath: importpath,
			}
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
		if w.skipDir(path, base) {
			return filepath.SkipDir
		}
		fi, err := os.Lstat(path)
		if err == nil && skipDir(fi) {
			return filepath.SkipDir
		}
		return nil
	}
	if typ == os.ModeSymlink {
		base := filepath.Base(path)
		if strings.HasPrefix(base, ".#") {
			return nil
		}
		fi, err := os.Lstat(path)
		if err != nil {
			return nil
		}
		if shouldTraverse(dir, fi) {
			return traverseLink
		}
		return nil
	}
	return nil
}

// vendorlessImportPath returns the devendorized version of the provided import path.
// e.g. "foo/bar/vendor/a/b" => "a/b"
func vendorlessImportPath(ipath string) string {
	// Devendorize for use in import statement.
	if i := strings.LastIndex(ipath, "/vendor/"); i >= 0 {
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
	sort.Sort(sort.StringSlice(paths))

	return paths, first
}

// TODO: Implement
func skipDir(fi os.FileInfo) bool { return false }

var visitedSymlinks struct {
	sync.Mutex
	m map[string]struct{}
}

func shouldTraverse(dir string, fi os.FileInfo) bool {
	path := filepath.Join(dir, fi.Name())
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	ts, err := os.Stat(target)
	if err != nil {
		return false
	}
	if !ts.IsDir() {
		return false
	}
	if skipDir(ts) {
		return false
	}

	realParent, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	realPath := filepath.Join(realParent, fi.Name())

	visitedSymlinks.Lock()
	if visitedSymlinks.m == nil {
		visitedSymlinks.m = make(map[string]struct{})
	}
	if _, ok := visitedSymlinks.m[realPath]; ok {
		visitedSymlinks.Unlock()
		return false
	}
	visitedSymlinks.m[realPath] = struct{}{}
	visitedSymlinks.Unlock()
	return true
}

// TODO (CEV): remove sort types if not used.

// byImportPathShortLength sorts by the short import path length, breaking ties on the
// import string itself.
type byImportPathShortLength []string

func (s byImportPathShortLength) Len() int { return len(s) }
func (s byImportPathShortLength) Less(i, j int) bool {
	vi, vj := s[i], s[j]
	return len(vi) < len(vj) || (len(vi) == len(vj) && vi < vj)
}
func (s byImportPathShortLength) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

// byLength sorts by string length.
type byLength []string

func (s byLength) Len() int           { return len(s) }
func (s byLength) Less(i, j int) bool { return len(s[i]) < len(s[j]) }
func (s byLength) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
