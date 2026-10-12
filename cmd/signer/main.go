package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GenerateKeyPair generates a cryptographically secure Ed25519 keypair.
// Returns base64-encoded public key (32 bytes) and private key (64 bytes).
func GenerateKeyPair() (pubBase64, privBase64 string, err error) {
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate ed25519 keys: %w", err)
	}
	return base64.StdEncoding.EncodeToString(pubKey), base64.StdEncoding.EncodeToString(privKey), nil
}

// SaveKeyPair writes the private key with 0600 permissions and the public key with 0644 permissions.
func SaveKeyPair(dir string, pubBase64, privBase64 string) (pubPath, privPath string, err error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", "", fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	privPath = filepath.Join(dir, "agent_ed25519_private.key")
	pubPath = filepath.Join(dir, "agent_ed25519_public.pub")

	// Strict 0600 mode for private key
	if err := os.WriteFile(privPath, []byte(privBase64+"\n"), 0600); err != nil {
		return "", "", fmt.Errorf("failed to write private key to %s: %w", privPath, err)
	}

	if err := os.WriteFile(pubPath, []byte(pubBase64+"\n"), 0644); err != nil {
		return "", "", fmt.Errorf("failed to write public key to %s: %w", pubPath, err)
	}

	return pubPath, privPath, nil
}

// LoadPrivateKey resolves private key either from raw base64 string or from a file path.
func LoadPrivateKey(keyOrPath string) (ed25519.PrivateKey, error) {
	keyStr := strings.TrimSpace(keyOrPath)
	if keyStr == "" {
		return nil, fmt.Errorf("private key cannot be empty")
	}

	// Check if keyOrPath is a valid file
	if fileBytes, err := os.ReadFile(keyStr); err == nil {
		keyStr = strings.TrimSpace(string(fileBytes))
	}

	decoded, err := base64.StdEncoding.DecodeString(keyStr)
	if err != nil {
		return nil, fmt.Errorf("invalid base64 encoding in private key: %w", err)
	}

	if len(decoded) == ed25519.PrivateKeySize {
		return ed25519.PrivateKey(decoded), nil
	} else if len(decoded) == ed25519.SeedSize {
		return ed25519.NewKeyFromSeed(decoded), nil
	}

	return nil, fmt.Errorf("invalid private key size %d bytes (expected %d or seed of %d bytes)", len(decoded), ed25519.PrivateKeySize, ed25519.SeedSize)
}

// LoadPublicKey resolves public key either from raw base64 string or from a file path.
func LoadPublicKey(keyOrPath string) (ed25519.PublicKey, error) {
	keyStr := strings.TrimSpace(keyOrPath)
	if keyStr == "" {
		return nil, fmt.Errorf("public key cannot be empty")
	}

	// Check if keyOrPath is a valid file
	if fileBytes, err := os.ReadFile(keyStr); err == nil {
		keyStr = strings.TrimSpace(string(fileBytes))
	}

	decoded, err := base64.StdEncoding.DecodeString(keyStr)
	if err != nil {
		return nil, fmt.Errorf("invalid base64 encoding in public key: %w", err)
	}

	if len(decoded) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid public key size %d bytes (expected %d bytes)", len(decoded), ed25519.PublicKeySize)
	}

	return ed25519.PublicKey(decoded), nil
}

// SignFile signs the contents of targetFile with the private key and returns base64 signature.
func SignFile(privKey ed25519.PrivateKey, targetFile string) (string, error) {
	data, err := os.ReadFile(targetFile)
	if err != nil {
		return "", fmt.Errorf("failed to read file to sign: %w", err)
	}

	sigBytes := ed25519.Sign(privKey, data)
	return base64.StdEncoding.EncodeToString(sigBytes), nil
}

// VerifyFile verifies that targetFile matches the base64 signature with the public key.
func VerifyFile(pubKey ed25519.PublicKey, targetFile, sigOrPath string) (bool, error) {
	data, err := os.ReadFile(targetFile)
	if err != nil {
		return false, fmt.Errorf("failed to read file to verify: %w", err)
	}

	sigStr := strings.TrimSpace(sigOrPath)
	if fileBytes, err := os.ReadFile(sigStr); err == nil {
		sigStr = strings.TrimSpace(string(fileBytes))
	}

	sigBytes, err := base64.StdEncoding.DecodeString(sigStr)
	if err != nil {
		return false, fmt.Errorf("invalid base64 encoding in signature: %w", err)
	}

	if len(sigBytes) != ed25519.SignatureSize {
		return false, fmt.Errorf("invalid signature size %d bytes (expected %d bytes)", len(sigBytes), ed25519.SignatureSize)
	}

	return ed25519.Verify(pubKey, data, sigBytes), nil
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "generate-keys":
		fs := flag.NewFlagSet("generate-keys", flag.ExitOnError)
		outDir := fs.String("out-dir", "", "Directory to save generated keys (e.g. .agent_update_keys)")
		_ = fs.Parse(os.Args[2:])

		pub, priv, err := GenerateKeyPair()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		if *outDir != "" {
			pubPath, privPath, err := SaveKeyPair(*outDir, pub, priv)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error saving keys: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Keys saved successfully:\n")
			fmt.Printf("  Public Key file  : %s\n", pubPath)
			fmt.Printf("  Private Key file : %s (mode 0600)\n\n", privPath)
		}

		fmt.Println("=== Base64 Keys ===")
		fmt.Printf("AGENT_PUBLIC_KEY=%s\n", pub)
		fmt.Printf("AGENT_PRIVATE_KEY=%s\n", priv)

	case "sign":
		fs := flag.NewFlagSet("sign", flag.ExitOnError)
		key := fs.String("key", "", "Private key string (Base64) or path to private key file")
		file := fs.String("file", "", "Target file to sign")
		outFile := fs.String("out", "", "Output signature file path (optional)")
		_ = fs.Parse(os.Args[2:])

		if *key == "" || *file == "" {
			fmt.Fprintln(os.Stderr, "Error: both -key and -file are required for signing")
			fs.Usage()
			os.Exit(1)
		}

		privKey, err := LoadPrivateKey(*key)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading private key: %v\n", err)
			os.Exit(1)
		}

		sig, err := SignFile(privKey, *file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error signing file: %v\n", err)
			os.Exit(1)
		}

		if *outFile != "" {
			if err := os.WriteFile(*outFile, []byte(sig+"\n"), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "Error writing signature to %s: %v\n", *outFile, err)
				os.Exit(1)
			}
			fmt.Printf("Signature written to: %s\n", *outFile)
		} else {
			fmt.Println(sig)
		}

	case "verify":
		fs := flag.NewFlagSet("verify", flag.ExitOnError)
		key := fs.String("key", "", "Public key string (Base64) or path to public key file")
		file := fs.String("file", "", "Target file to verify")
		sig := fs.String("sig", "", "Signature string (Base64) or path to signature file")
		_ = fs.Parse(os.Args[2:])

		if *key == "" || *file == "" || *sig == "" {
			fmt.Fprintln(os.Stderr, "Error: -key, -file, and -sig are required for verification")
			fs.Usage()
			os.Exit(1)
		}

		pubKey, err := LoadPublicKey(*key)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading public key: %v\n", err)
			os.Exit(1)
		}

		ok, err := VerifyFile(pubKey, *file, *sig)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Verification error: %v\n", err)
			os.Exit(1)
		}

		if !ok {
			fmt.Fprintln(os.Stderr, "FAILED: Signature does not match the binary file!")
			os.Exit(2)
		}

		fmt.Println("OK: Signature verified successfully.")

	case "key-info":
		fs := flag.NewFlagSet("key-info", flag.ExitOnError)
		key := fs.String("key", "", "Key string (Base64) or path to key file")
		_ = fs.Parse(os.Args[2:])

		if *key == "" {
			fmt.Fprintln(os.Stderr, "Error: -key is required")
			os.Exit(1)
		}

		// Try loading as public key first
		if pubKey, err := LoadPublicKey(*key); err == nil {
			h := sha256.Sum256(pubKey)
			fmt.Printf("Type        : Ed25519 Public Key (32 bytes)\n")
			fmt.Printf("Base64      : %s\n", base64.StdEncoding.EncodeToString(pubKey))
			fmt.Printf("SHA256 Fingerprint: %x\n", h[:])
			return
		}

		// Try loading as private key
		if privKey, err := LoadPrivateKey(*key); err == nil {
			pubKey := privKey.Public().(ed25519.PublicKey)
			h := sha256.Sum256(pubKey)
			fmt.Printf("Type        : Ed25519 Private Key (%d bytes)\n", len(privKey))
			fmt.Printf("Matching Public Key Base64 : %s\n", base64.StdEncoding.EncodeToString(pubKey))
			fmt.Printf("SHA256 Fingerprint         : %x\n", h[:])
			return
		}

		fmt.Fprintln(os.Stderr, "Error: provided key is neither a valid Ed25519 public key nor private key")
		os.Exit(1)

	default:
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("RasnuOS Agent Ed25519 Signer Utility")
	fmt.Println("Usage: signer <command> [options]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  generate-keys  Generate a new Ed25519 keypair")
	fmt.Println("  sign           Sign a binary file with private key")
	fmt.Println("  verify         Verify a binary file against a signature and public key")
	fmt.Println("  key-info       Display metadata for a key")
}
