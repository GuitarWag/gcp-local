package tlsx

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
)

// TrustInstall adds the generated cert to the platform trust store.
// macOS: uses `security add-trusted-cert` against the login keychain.
// Linux: prints manual instructions and returns nil (we don't poke NSS DBs).
// Windows: not supported.
func TrustInstall(p Paths, out io.Writer) error {
	if !exists(p.CertFile) {
		return fmt.Errorf("cert not found at %s — run `gcp-local start --tls` first", p.CertFile)
	}
	switch runtime.GOOS {
	case "darwin":
		return darwinTrust(p, out, true)
	case "linux":
		printLinuxInstructions(p, out, true)
		return nil
	case "windows":
		return fmt.Errorf("trust install: not supported on Windows")
	default:
		return fmt.Errorf("trust install: unsupported platform %s", runtime.GOOS)
	}
}

// TrustUninstall removes the cert from the platform trust store.
func TrustUninstall(p Paths, out io.Writer) error {
	switch runtime.GOOS {
	case "darwin":
		return darwinTrust(p, out, false)
	case "linux":
		printLinuxInstructions(p, out, false)
		return nil
	case "windows":
		return fmt.Errorf("trust uninstall: not supported on Windows")
	default:
		return fmt.Errorf("trust uninstall: unsupported platform %s", runtime.GOOS)
	}
}

func darwinTrust(p Paths, out io.Writer, install bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("user home dir: %w", err)
	}
	keychain := home + "/Library/Keychains/login.keychain-db"
	if _, err := os.Stat(keychain); err != nil {
		// Older macOS keeps it under login.keychain.
		keychain = home + "/Library/Keychains/login.keychain"
	}

	var cmd *exec.Cmd
	if install {
		if !exists(p.CertFile) {
			return fmt.Errorf("cert not found at %s", p.CertFile)
		}
		cmd = exec.Command("security", "add-trusted-cert", "-r", "trustRoot", "-k", keychain, p.CertFile)
	} else {
		cmd = exec.Command("security", "remove-trusted-cert", p.CertFile)
	}

	combined, err := cmd.CombinedOutput()
	if err != nil {
		// macOS often returns exit 1 with "User interaction is not allowed"
		// when run non-interactively. Surface a clean error instead of a
		// raw exit status so callers can tell the user what to do.
		fmt.Fprintf(out, "%s\n", combined)
		if install {
			return fmt.Errorf("`security add-trusted-cert` failed (may require an interactive terminal / sudo): %w", err)
		}
		return fmt.Errorf("`security remove-trusted-cert` failed: %w", err)
	}
	if install {
		fmt.Fprintf(out, "trust install: added %s to %s\n", p.CertFile, keychain)
	} else {
		fmt.Fprintf(out, "trust uninstall: removed %s from %s\n", p.CertFile, keychain)
	}
	return nil
}

func printLinuxInstructions(p Paths, out io.Writer, install bool) {
	if install {
		fmt.Fprintf(out, `Linux trust install (manual — distro specific):

Debian/Ubuntu:
  sudo cp %s /usr/local/share/ca-certificates/gcp-local.crt
  sudo update-ca-certificates

Fedora/RHEL:
  sudo cp %s /etc/pki/ca-trust/source/anchors/gcp-local.crt
  sudo update-ca-trust

Browsers (Firefox/Chromium) use their own NSS database; add via
  certutil -d sql:$HOME/.pki/nssdb -A -t "CT,C,C" -n gcp-local -i %s

The emulator does not manage NSS programmatically. Skipping.
`, p.CertFile, p.CertFile, p.CertFile)
	} else {
		fmt.Fprintf(out, `Linux trust uninstall (manual — distro specific):

Debian/Ubuntu:
  sudo rm /usr/local/share/ca-certificates/gcp-local.crt
  sudo update-ca-certificates --fresh

Fedora/RHEL:
  sudo rm /etc/pki/ca-trust/source/anchors/gcp-local.crt
  sudo update-ca-trust

Browsers:
  certutil -d sql:$HOME/.pki/nssdb -D -n gcp-local
`)
	}
}
