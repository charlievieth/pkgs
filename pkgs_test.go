package pkgs

import (
	"go/build"
	"os"
	"strings"
	"testing"
)

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
