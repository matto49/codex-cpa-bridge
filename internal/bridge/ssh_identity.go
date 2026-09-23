package bridge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func bridgeClientIdentityPath(m Manifest) string {
	return filepath.Join(m.Runtime.StateDir, "ssh", "client_ed25519")
}

func regularFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

// sshd StrictModes checks authorized_keys ancestors through the account home,
// or through the filesystem root when the key is outside that home. It does
// not reject a private home merely because an ancestor above it is foreign-
// owned (a common layout for mounted home volumes).
func validateManagedSSHPath(path string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return err
	}
	directory := filepath.Clean(filepath.Dir(path))
	for {
		if _, err := os.Stat(directory); errors.Is(err, os.ErrNotExist) {
			parent := filepath.Dir(directory)
			if parent == directory {
				return err
			}
			directory = parent
			continue
		} else if err != nil {
			return err
		}
		break
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return err
	}
	stopAtHome := false
	if relative, err := filepath.Rel(home, resolved); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		stopAtHome = true
	}
	for current := resolved; ; current = filepath.Dir(current) {
		info, err := os.Stat(current)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
			return fmt.Errorf("insecure SSH path %s: sshd StrictModes requires private ancestor directories; choose a directory under your home", current)
		}
		if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Uid != 0 && stat.Uid != uint32(os.Getuid()) {
			return fmt.Errorf("insecure SSH path %s: directory must be owned by this user or root", current)
		}
		if stopAtHome && current == home {
			return nil
		}
		if parent := filepath.Dir(current); parent == current {
			return nil
		}
	}
}

// generateBridgeClientIdentity creates a dedicated client key only when neither
// half of the pair exists. Existing keys, including partial pairs, are never
// replaced automatically.
func generateBridgeClientIdentity(m Manifest) (string, error) {
	private := bridgeClientIdentityPath(m)
	public := private + ".pub"
	for _, path := range []string{private, public} {
		if _, err := os.Lstat(path); err == nil {
			return "", fmt.Errorf("bridge SSH identity already exists at %s; inspect it before retrying", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		return "", errors.New("ssh-keygen is required to create a bridge SSH identity")
	}
	directory := filepath.Dir(private)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	if info, err := os.Lstat(directory); err != nil || !info.IsDir() {
		return "", fmt.Errorf("bridge SSH identity directory is not a regular directory: %s", directory)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", private)
	if output, err := command.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("bridge SSH key generation timed out: %w", ctx.Err())
		}
		return "", fmt.Errorf("cannot generate bridge SSH identity: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if !regularFile(private) || !regularFile(public) {
		return "", errors.New("ssh-keygen did not create a regular key pair")
	}
	if err := os.Chmod(private, 0o600); err != nil {
		return "", err
	}
	return public, nil
}
