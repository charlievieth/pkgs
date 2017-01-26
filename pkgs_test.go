package pkgs

import (
	"go/build"
	"os"
	"testing"
)

func BenchmarkImport(b *testing.B) {
	pwd, err := os.Getwd()
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := Walk(&build.Default, pwd)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWalk(b *testing.B) {
	pwd, err := os.Getwd()
	if err != nil {
		b.Fatal(err)
	}
	ctxt := &build.Default
	srcDirs := ctxt.SrcDirs()
	m := make(map[string]*Walker)
	for _, s := range srcDirs {
		w, err := newWalker(pwd, s, ctxt)
		if err != nil {
			b.Fatal(err)
		}
		m[s] = w
	}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for _, s := range srcDirs {
			if err := m[s].Update(); err != nil {
				b.Fatal(err)
			}
		}

	}
	return
}
