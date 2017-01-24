package main

import (
	"fmt"
	"go/build"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"git.vieth.io/buildutil"
)

type byImportPath []*Pkg

func (b byImportPath) Len() int           { return len(b) }
func (b byImportPath) Less(i, j int) bool { return b[i].ImportPath < b[j].ImportPath }
func (b byImportPath) Swap(i, j int)      { b[i], b[j] = b[j], b[i] }

type Pkg struct {
	Name            string // package name
	Dir             string // absolute file path to pkg directory ("/usr/lib/go/src/net/http")
	ImportPath      string // full pkg import path ("net/http", "foo/bar/vendor/a/b")
	ImportPathShort string // vendorless import path ("net/http", "a/b")
	Vendor          bool
	ord             uint32
}

// vendorlessImportPath returns the devendorized version of the provided import path.
// e.g. "foo/bar/vendor/a/b" => "a/b"
func vendorlessImportPath(ipath string) (string, bool) {
	// Devendorize for use in import statement.
	if i := strings.LastIndex(ipath, "/vendor/"); i >= 0 {
		return ipath[i+len("/vendor/"):], true
	}
	if strings.HasPrefix(ipath, "vendor/") {
		return ipath[len("vendor/"):], true
	}
	return ipath, false
}

// TODO: Implement
func skipDir(fi os.FileInfo) bool {
	return false
}

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
	defer visitedSymlinks.Unlock()
	if visitedSymlinks.m == nil {
		visitedSymlinks.m = make(map[string]struct{})
	}
	if _, ok := visitedSymlinks.m[realPath]; ok {
		return false
	}
	visitedSymlinks.m[realPath] = struct{}{}
	return true
}

// TODO: rename
type Packages struct {
	ctxt     *build.Context
	srcDirs  []string
	walkers  map[string]*Walker
	freq     time.Duration
	updateMu sync.Mutex
}

// TODO:
//  - GOROOT: walk once?
//  - Scan pkg dir?
//  - Re-write fastwalk to skip known good dirs
//
func NewPackages(ctxt *build.Context, updateFreq time.Duration) *Packages {
	if ctxt == nil {
		ctxt = &build.Default
	}
	srcDirs := ctxt.SrcDirs()
	walkers := make(map[string]*Walker, len(srcDirs))
	for _, s := range srcDirs {
		walkers[s] = newWalker(s, ctxt)
	}
	p := &Packages{
		ctxt:    ctxt,
		srcDirs: srcDirs,
		walkers: walkers,
		freq:    updateFreq,
	}
	return p
}

func (p *Packages) ListImports(importDir string) ([]string, error) {
	err := p.Update()
	var paths []string
	for _, w := range p.walkers {
		paths = w.appendPkgImportPaths(paths, importDir)
	}
	sort.Strings(paths)
	n := 0
	for i := 1; i < len(paths); i++ {
		if paths[n] != paths[i] {
			n++
			paths[n] = paths[i]
		}
	}
	return paths[:n+1], err
}

func (p *Packages) Update() (first error) {
	p.updateMu.Lock()
	for _, w := range p.walkers {
		if err := w.Update(); err != nil && first == nil {
			first = err
		}
	}
	p.updateMu.Unlock()
	return
}

type Walker struct {
	srcDir   string
	pkgDir   string
	ctxt     *build.Context
	pkgs     map[string]*Pkg // abs dir path => *pk	g
	mu       sync.RWMutex
	updateMu sync.Mutex
	ord      uint32 // oridinal - used for trimming removed pkgs
}

func newWalker(srcDir string, ctxt *build.Context) *Walker {
	var pkgtargetroot string
	switch ctxt.Compiler {
	case "gccgo":
		pkgtargetroot = "pkg/gccgo_" + ctxt.GOOS + "_" + ctxt.GOARCH
	case "gc":
		pkgtargetroot = "pkg/" + ctxt.GOOS + "_" + ctxt.GOARCH
	default:
		// TODO: remove panic
		panic(fmt.Errorf("pkgs: unknown compiler %q", ctxt.Compiler))
	}
	if ctxt.InstallSuffix != "" {
		pkgtargetroot += "_" + ctxt.InstallSuffix
	}
	pkgDir := srcDir[:len(srcDir)-len("src")] + pkgtargetroot
	return &Walker{
		srcDir: filepath.ToSlash(srcDir),
		pkgDir: filepath.ToSlash(pkgDir),
		ctxt:   ctxt,
		pkgs:   make(map[string]*Pkg),
	}
}

func (w *Walker) appendPkgImportPaths(paths []string, importDir string) []string {
	w.mu.Lock()
	for _, p := range w.pkgs {
		// TODO: ensure no vendor directories are included if importDir == ""
		if !p.Vendor || (importDir != "" && hasPathPrefix(p.ImportPath, importDir)) {
			paths = append(paths, p.ImportPath)
		}
	}
	w.mu.Unlock()
	return paths
}

func (w *Walker) listPkgImportPaths(importDir string, unique bool) []string {
	paths := w.appendPkgImportPaths([]string{}, importDir)
	if !unique {
		return paths
	}
	sort.Strings(paths)
	n := 0
	for i := 0; i < len(paths); i++ {
		if paths[n] != paths[i] {
			paths[n] = paths[i]
			n++
		}
	}
	return paths[:n]
}

// importDir must be the Go import path of the package
func (w *Walker) listPkgs(importDir string, sort bool) []*Pkg {
	var pkgs []*Pkg
	for _, p := range w.pkgs {
		if !p.Vendor || hasPathPrefix(p.ImportPath, importDir) {
			pkgs = append(pkgs, p)
		}
	}
	return pkgs
}

func (w *Walker) Update() error {
	w.updateMu.Lock()
	defer w.updateMu.Unlock()

	ord := atomic.AddUint32(&w.ord, 1)
	if err := fastWalk(w.srcDir, w.Walk); err != nil {
		return err
	}

	// TODO: Add 'AllowBinary' mode so that pkgs are not
	// included if the source code has been deleted.
	if !strings.HasPrefix(w.srcDir, w.ctxt.GOROOT) {
		if err := fastWalk(w.pkgDir, w.WalkPkg); err != nil {
			return err
		}
	}

	w.mu.Lock()
	for s, p := range w.pkgs {
		if p.ord != ord {
			delete(w.pkgs, s)
		}
	}
	w.mu.Unlock()
	return nil
}

func (w *Walker) seen(dirname string) bool {
	var p *Pkg
	w.mu.RLock()
	if w.pkgs != nil {
		if p = w.pkgs[dirname]; p != nil {
			atomic.StoreUint32(&p.ord, w.ord)
		}
	}
	w.mu.RUnlock()
	return p != nil
}

func (w *Walker) WalkPkg(path string, typ os.FileMode) error {
	if !typ.IsRegular() || !strings.HasSuffix(path, ".a") {
		return nil
	}
	// $GOPATH/pkg/GOOS_GOARCH/... => $GOPATH/src/...
	path = w.srcDir + path[len(w.pkgDir):]

	// definately works
	// dir := filepath.Join(filepath.Dir(path), strings.TrimSuffix(filepath.Base(path), ".a"))

	// appears to work
	dir := strings.TrimSuffix(path, ".a")

	if w.seen(dir) {
		return nil
	}
	w.mu.Lock()
	if _, dup := w.pkgs[dir]; !dup {
		importpath := filepath.ToSlash(dir[len(w.srcDir)+len("/"):])
		importPathShort, vendor := vendorlessImportPath(importpath)
		w.pkgs[dir] = &Pkg{
			ImportPath:      importpath,
			ImportPathShort: importPathShort,
			Dir:             dir,
			Name:            filepath.Base(dir), // don't use path - must trim '.a'
			Vendor:          vendor,
			ord:             w.ord,
		}
	}
	w.mu.Unlock()

	return nil
}

func (w *Walker) Walk(path string, typ os.FileMode) error {
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
			importpath := filepath.ToSlash(dir[len(w.srcDir)+len("/"):])
			importPathShort, vendor := vendorlessImportPath(importpath)
			w.pkgs[dir] = &Pkg{
				ImportPath:      importpath,
				ImportPathShort: importPathShort,
				Dir:             dir,
				Name:            name,
				Vendor:          vendor,
				ord:             w.ord,
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
	}
	return nil
}

func main() {
	p := NewPackages(nil, 0)
	t := time.Now()
	s, err := p.ListImports("")
	d := time.Since(t)
	if err != nil {
		Fatal(err)
	}

	fmt.Println("len:", len(s), d, d/time.Duration(len(s)))
}

func PrintDirNames(p *Packages) {
	var dirs []string
	for _, w := range p.walkers {
		for d := range w.pkgs {
			dirs = append(dirs, d)
		}
	}
	sort.Strings(dirs)
	for _, s := range dirs {
		fmt.Println(s)
	}
}

func Fatal(err interface{}) {
	if err == nil {
		return
	}
	_, file, line, ok := runtime.Caller(1)
	if ok {
		if s := os.Getenv("GOPATH"); s != "" {
			s += string(os.PathSeparator) + "src"
			file = strings.TrimPrefix(file, s)
			file = strings.TrimPrefix(file, string(os.PathSeparator))
		}
	}
	switch err.(type) {
	case error, string, fmt.Stringer:
		if ok {
			fmt.Fprintf(os.Stderr, "Error (%s:%d): %s", file, line, err)
		} else {
			fmt.Fprintf(os.Stderr, "Error: %s", err)
		}
	default:
		if ok {
			fmt.Fprintf(os.Stderr, "Error (%s:%d): %#v\n", file, line, err)
		} else {
			fmt.Fprintf(os.Stderr, "Error: %#v\n", err)
		}
	}
	os.Exit(1)
}
