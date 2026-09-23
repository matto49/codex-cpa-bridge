package bridge

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// createPrivateFile publishes a new 0600 file without replacing a file or
// symlink that appears between preview and apply.
func createPrivateFile(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".bridge-new-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(content); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Link(file.Name(), path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("target appeared at %s; refusing to overwrite it", path)
		}
		return err
	}
	return nil
}
