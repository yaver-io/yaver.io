package main

// keys_cmd.go — `yaver keys`: the "auto" actions the stores concierge points
// at. Generate + safely store the signing material a normie otherwise fumbles:
//
//   yaver keys init --platform android   keytool upload keystore → vault + SHA-1
//   yaver keys init --platform ios        EC private key + CSR (for the ASC cert)
//   yaver keys sha1                        print the keystore's SHA-1
//   yaver keys signin-google               package + SHA-1 to paste into Google Cloud
//
// Secrets land in the vault (encrypted, never Convex). The keystore file lives
// under ~/.yaver/keys — back it up via the P2P/cloud vault sync (losing an
// Android upload key historically meant you could never update the app; opt
// into Play App Signing so Google holds the real key as a safety net).

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

func keysDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".yaver", "keys")
}

// genPassword returns a URL-safe random password of ~n bytes of entropy.
func genPassword(nBytes int) (string, error) {
	if nBytes < 16 {
		nBytes = 24
	}
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// generateECKeyCSR makes a P-256 private key + a CSR for it (PEM strings).
// Apple's distribution cert is created from a CSR; we generate both locally so
// the private key never leaves the machine.
func generateECKeyCSR(commonName string) (keyPEM, csrPEM string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", "", err
	}
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))

	tmpl := x509.CertificateRequest{
		Subject:            pkix.Name{CommonName: commonName},
		SignatureAlgorithm: x509.ECDSAWithSHA256,
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &tmpl, key)
	if err != nil {
		return "", "", err
	}
	csrPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER}))
	return keyPEM, csrPEM, nil
}

var sha1Re = regexp.MustCompile(`(?i)SHA-?1:\s*([0-9A-F:]{20,})`)

// extractSHA1 pulls the SHA-1 fingerprint out of `keytool -list -v` output.
func extractSHA1(keytoolOut string) string {
	m := sha1Re.FindStringSubmatch(keytoolOut)
	if len(m) == 2 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func vaultSetWith(vs *VaultStore, project, name, value, category string) {
	if err := vs.Set(VaultEntry{Name: name, Project: project, Value: value, Category: category}); err != nil {
		fmt.Printf("    (vault write failed for %s: %v)\n", name, err)
	}
}

func runKeys(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: yaver keys <init|sha1|signin-google> [--project P] [--platform ios|android] [--path DIR]")
		return
	}
	sub := args[0]
	rest := args[1:]
	project, platform, path := "", "", "."
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--project":
			if i+1 < len(rest) {
				project = rest[i+1]
				i++
			}
		case "--platform":
			if i+1 < len(rest) {
				platform = rest[i+1]
				i++
			}
		case "--path":
			if i+1 < len(rest) {
				path = rest[i+1]
				i++
			}
		}
	}
	if project == "" {
		project = "default"
	}

	switch sub {
	case "init":
		switch platform {
		case "android":
			keysInitAndroid(project, path)
		case "ios":
			keysInitIOS(project, path)
		default:
			fmt.Fprintln(os.Stderr, "Error: --platform ios|android required")
		}
	case "sha1":
		keysSHA1(project)
	case "signin-google":
		keysSignInGoogle(project, path)
	default:
		fmt.Fprintf(os.Stderr, "Unknown keys subcommand %q\n", sub)
	}
}

func androidKeystorePath(project string) string {
	return filepath.Join(keysDir(), project+"-upload.keystore")
}

func keysInitAndroid(project, path string) {
	ksPath := androidKeystorePath(project)
	if _, err := os.Stat(ksPath); err == nil {
		fmt.Printf("Keystore already exists: %s\n", ksPath)
		keysSHA1(project)
		return
	}
	if _, err := exec.LookPath("keytool"); err != nil {
		fmt.Fprintln(os.Stderr, "keytool not found — install a JDK 17 (brew install openjdk@17).")
		return
	}
	// SAFETY: the keystore is created with a RANDOM password. If we can't
	// store it in the vault, that password is lost and the keystore is
	// useless forever (you could never update the app). So require a
	// writable vault BEFORE generating — never create an orphaned keystore.
	vs, err := openVaultOptional()
	if err != nil || vs == nil {
		fmt.Fprintln(os.Stderr, "Vault required to safely store the keystore password (else it's lost forever). Run `yaver auth` first.")
		return
	}
	if err := os.MkdirAll(keysDir(), 0700); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir %s: %v\n", keysDir(), err)
		return
	}
	pw, err := genPassword(24)
	if err != nil {
		fmt.Fprintf(os.Stderr, "password gen: %v\n", err)
		return
	}
	alias := project + "-upload"
	cfg := readAppConfig(path)
	cn := cfg.Expo.Name
	if cn == "" {
		cn = project
	}
	cmd := exec.Command("keytool", "-genkeypair", "-keystore", ksPath, "-alias", alias,
		"-keyalg", "RSA", "-keysize", "2048", "-validity", "10000",
		"-storepass", pw, "-keypass", pw,
		"-dname", fmt.Sprintf("CN=%s, OU=Mobile, O=%s, C=US", cn, cn))
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "keytool genkeypair failed: %v\n%s\n", err, out)
		return
	}
	fmt.Printf("✓ Created upload keystore: %s\n", ksPath)
	vaultSetWith(vs, project, "ANDROID_KEYSTORE_PASSWORD", pw, "signing-key")
	vaultSetWith(vs, project, "ANDROID_KEY_ALIAS", alias, "signing-key")
	vaultSetWith(vs, project, "ANDROID_KEY_PASSWORD", pw, "signing-key")
	vaultSetWith(vs, project, "ANDROID_KEYSTORE_PATH", ksPath, "signing-key")
	fmt.Println("  Stored keystore passwords in your vault.")
	fmt.Println("  ⚠ Opt into Play App Signing on first upload — then a lost upload key is recoverable.")
	keysSHA1(project)
}

func keysSHA1(project string) {
	ksPath := androidKeystorePath(project)
	if _, err := os.Stat(ksPath); err != nil {
		fmt.Fprintf(os.Stderr, "No keystore for %q — run: yaver keys init --project %s --platform android\n", project, project)
		return
	}
	pw := ""
	if vs, err := openVaultOptional(); err == nil && vs != nil {
		if e, err := vs.Get(project, "ANDROID_KEYSTORE_PASSWORD"); err == nil {
			pw = e.Value
		}
	}
	alias := project + "-upload"
	args := []string{"-list", "-v", "-keystore", ksPath, "-alias", alias}
	if pw != "" {
		args = append(args, "-storepass", pw)
	}
	out, _ := exec.Command("keytool", args...).CombinedOutput()
	sha := extractSHA1(string(out))
	if sha == "" {
		fmt.Fprintf(os.Stderr, "Couldn't read SHA-1 (vault locked? pass the storepass).\n")
		return
	}
	fmt.Printf("  SHA-1: %s\n", sha)
}

func keysInitIOS(project, path string) {
	vs, err := openVaultOptional()
	if err != nil || vs == nil {
		fmt.Fprintln(os.Stderr, "Vault required to store the iOS distribution key. Run `yaver auth` first.")
		return
	}
	cfg := readAppConfig(path)
	cn := cfg.Expo.Name
	if cn == "" {
		cn = project
	}
	keyPEM, csrPEM, err := generateECKeyCSR(cn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "key/CSR generation failed: %v\n", err)
		return
	}
	if err := os.MkdirAll(keysDir(), 0700); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir: %v\n", err)
		return
	}
	csrPath := filepath.Join(keysDir(), project+"-dist.csr")
	_ = os.WriteFile(csrPath, []byte(csrPEM), 0600)
	vaultSetWith(vs, project, "IOS_DIST_KEY_PEM", keyPEM, "signing-key")
	fmt.Printf("✓ Generated iOS distribution key (in vault) + CSR: %s\n", csrPath)
	fmt.Println("  Next: Yaver creates the distribution certificate from this CSR via the")
	fmt.Println("  App Store Connect API once your ASC key is in the vault (see `yaver stores apple-asc-key`).")
}

func keysSignInGoogle(project, path string) {
	cfg := readAppConfig(path)
	pkg := cfg.Expo.Android.Package
	fmt.Println("Google Sign-In — paste these into Google Cloud → Credentials → OAuth client (Android):")
	fmt.Printf("  Package name: %s\n", dashIfEmpty(pkg))
	keysSHA1(project)
	fmt.Println("  Then create iOS (bundle id) + Web client IDs as needed.")
	fmt.Println("  Open: https://console.cloud.google.com/apis/credentials")
}
