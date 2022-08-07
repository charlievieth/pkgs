package pkgs

import (
	"bytes"
	"fmt"
	"go/build"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
)

const skipDirImportDir = "/go/src/pkg"

var skipDirTests = map[string]bool{
	"/go/src/other":                 false,
	"/go/src/pkg":                   false,
	"/go/src/pkg/vendor":            false,
	"/go/src/pkg/vendor/foo":        false,
	"/go/src/pkg/vendor/foo/vendor": true,
	"/go/src/other/vendor":          true,
}

func TestSkipDir(t *testing.T) {
	for path, exp := range skipDirTests {
		ok := skipDir(skipDirImportDir, path, filepath.Base(path))
		if ok != exp {
			t.Errorf("TestSkipDir (%s) expected: %t got: %t", path, exp, ok)
		}
	}
}

var stdLibPkgs struct {
	pkgs []string
	err  error
	once sync.Once
}

func loadStdLibPkgs(t *testing.T) []string {
	stdLibPkgs.once.Do(func() {
		exclude := regexp.MustCompile(`(^|/)(builtin|cmd|internal)(/|$)`)

		cmd := exec.Command("go", "list", "./...")
		cmd.Dir = filepath.Join(runtime.GOROOT(), "src")
		out, err := cmd.CombinedOutput()
		out = bytes.TrimSpace(out)
		if err != nil {
			stdLibPkgs.err = fmt.Errorf("%w: %s", err, out)
		}

		var pkgs []string
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			if !exclude.MatchString(line) {
				pkgs = append(pkgs, line)
			}
		}
		sort.Strings(pkgs)
		stdLibPkgs.pkgs = pkgs
	})
	if stdLibPkgs.err != nil {
		t.Fatal(stdLibPkgs.err)
	}
	return stdLibPkgs.pkgs
}

func comparePkgs(t *testing.T, got, want []string) {
	if reflect.DeepEqual(got, want) {
		return
	}
	m1 := make(map[string]bool, len(got))
	m2 := make(map[string]bool, len(want))
	for _, s := range got {
		m1[s] = true
	}
	for _, s := range want {
		m2[s] = true
	}
	var missing []string
	var extra []string
	for s := range m1 {
		if !m2[s] {
			extra = append(extra, s)
		}
	}
	for s := range m2 {
		if !m1[s] {
			missing = append(missing, s)
		}
	}
	t.Errorf("Package lists are not equal:\nExtra: %q\nMissing: %q", extra, missing)
}

func TestWalkStdLib(t *testing.T) {
	want := loadStdLibPkgs(t)

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	ctxt := build.Default
	ctxt.GOPATH = ""
	got, err := Walk(&ctxt, wd)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)

	comparePkgs(t, got, want)
}

func TestWalkGOPATH(t *testing.T) {
	files := map[string]string{
		"m/m.go":             "package main\n\nfunc main() { println(\"hello\") }\n",
		"p1/p1.go":           "package p1\n",
		"p1/gen1.go":         "//go:build generate1\n\npackage main\n\nfunc main() { println(\"hello\") }\n",
		"p1/gen2.go":         "//go:build generate2\n\npackage main\n\nfunc main() { println(\"hello\") }\n",
		"p1/testdata/t/t.go": "package t\n",
		"p1/vendor/v1/v1.go": "package v1\n",
		"p2/p2.go":           "package p2\n",
		"p2/vendor/v2/v2.go": "package v2\n",
	}

	gopath := t.TempDir()
	for name, data := range files {
		path := filepath.Join(gopath, "src", name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}

	want := []string{
		"p1",
		"p2",
		"v1",
	}
	want = append(want, loadStdLibPkgs(t)...)
	sort.Strings(want)

	importDir := filepath.Join(gopath, "src", "p1")
	ctxt := build.Default
	ctxt.GOPATH = gopath
	got, err := Walk(&ctxt, importDir)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)

	comparePkgs(t, got, want)
}

func BenchmarkImport(b *testing.B) {
	pwd, err := os.Getwd()
	if err != nil {
		b.Fatal(err)
	}
	ctxt := build.Default
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := Walk(&ctxt, pwd)
		if err != nil {
			b.Fatal(err)
		}
	}
}

const VendorImportPath = "GOPATH/src/github.com/USERNAME/GoSubl/src/gosubli.me/margo/vendor/github.com/charlievieth/gocode/vendor/github.com/golang/groupcache/lru/lru"

func BenchmarkLastVendor(b *testing.B) {
	for i := 0; i < b.N; i++ {
		lastVendor(VendorImportPath)
	}
}

func BenchmarkLastVendor_Base(b *testing.B) {
	for i := 0; i < b.N; i++ {
		strings.LastIndex(VendorImportPath, "/vendor/")
	}
}
