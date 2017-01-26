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
