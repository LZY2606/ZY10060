package store

import (
	"os"
	"path/filepath"
)

func fsyncDir(path string) error {
	dir := filepath.Dir(path)
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
