package main

import (
	"path/filepath"
	"runtime"
	"testing"
)

func BenchmarkFastwalk(b *testing.B) {
	srcDir := filepath.Join(runtime.GOROOT(), "src")
	for i := 0; i < b.N; i++ {
		w := Walker{
			srcDir: srcDir,
			pkgs:   make(map[string]*Pkg),
		}
		if err := fastWalk(w.srcDir, w.Walk); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFastwalk_X(b *testing.B) {
	srcDir := filepath.Join(runtime.GOROOT(), "src")
	w := Walker{
		srcDir: srcDir,
		pkgs:   make(map[string]*Pkg),
	}
	for i := 0; i < b.N; i++ {
		if err := fastWalk(w.srcDir, w.Walk); err != nil {
			b.Fatal(err)
		}
	}
}
