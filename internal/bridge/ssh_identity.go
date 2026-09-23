package bridge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func bridgeClientIdentityPath(m Manifest) string {
	return filepath.Join(m.Runtime.StateDir, "ssh", "client_ed25519")
}

func regularFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
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
