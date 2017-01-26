// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build appengine !linux,!darwin,!freebsd,!openbsd,!netbsd

package pkgs

import (
	"os"

	"github.com/charlievieth/fs"
)

// readDir calls fn for each directory entry in dirName.
// It does not descend into directories or follow symlinks.
// If fn returns a non-nil error, readDir returns with that error
// immediately.
func readDir(dirName string, fn func(dirName, entName string, typ os.FileMode) error) error {
	fis, err := readDirEnts(dirName)
	if err != nil {
		return err
	}
	for _, fi := range fis {
		if err := fn(dirName, fi.Name(), fi.Mode()&os.ModeType); err != nil {
			return err
		}
	}
	return nil
}

func readDirEnts(dirname string) ([]os.FileInfo, error) {
	f, err := fs.Open(dirname)
	if err != nil {
		return nil, err
	}
	list, err := f.Readdir(-1)
	f.Close()
	if err != nil {
		return nil, err
	}
	return list, nil
}
