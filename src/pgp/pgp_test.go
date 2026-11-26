package pgp

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateWrapEncryptAndRotateKeypair(t *testing.T) {
	secret := bytes.Repeat([]byte{0x42}, 32)
	now := time.Now().UTC().Truncate(time.Second)
	first, err := Generate("Git Messages Security", "security@example.test", secret, now)
	if err != nil {
		t.Fatalf("generate PGP keypair: %v", err)
	}
	if len(first.Metadata.Fingerprint) != 40 || first.Metadata.CreatedAt.IsZero() || !first.Metadata.ExpiresAt.Equal(now.AddDate(2, 0, 0)) {
		t.Fatalf("generated key metadata = %+v", first.Metadata)
	}
	if first.Metadata.Identity != "Git Messages Security <security@example.test>" {
		t.Fatalf("generated identity = %q", first.Metadata.Identity)
	}
	if _, err := MetadataForPublic(first.Public); err != nil {
		t.Fatalf("validate generated public key: %v", err)
	}
	private, err := DecryptPrivate(first.EncryptedPrivate, secret)
	if err != nil {
		t.Fatalf("unwrap generated private key: %v", err)
	}
	if _, err := DecryptPrivate(first.EncryptedPrivate, bytes.Repeat([]byte{0x24}, 32)); err == nil {
		t.Fatal("private key decrypted with the wrong installation secret")
	}
	exported, err := ExportPrivate(first.EncryptedPrivate, secret)
	if err != nil {
		t.Fatalf("export private key: %v", err)
	}
	rewrapped, err := RewrapPrivateKey(first.EncryptedPrivate, secret, bytes.Repeat([]byte{0x24}, 32))
	if err != nil {
		t.Fatalf("rewrap private key: %v", err)
	}
	rewrappedPrivate, err := ExportPrivate(rewrapped, bytes.Repeat([]byte{0x24}, 32))
	if err != nil || !bytes.Equal(exported, rewrappedPrivate) {
		t.Fatalf("rewrapped private key changed bytes or failed validation: err=%v", err)
	}
	message := []byte("confidential security report")
	encrypted, err := Encrypt(first.Public, message)
	if err != nil {
		t.Fatalf("encrypt report: %v", err)
	}
	decrypted, err := Decrypt(encrypted, first.EncryptedPrivate, secret)
	if err != nil {
		t.Fatalf("decrypt report: %v", err)
	}
	if !bytes.Equal(decrypted, message) {
		t.Fatalf("decrypted report = %q, want %q", decrypted, message)
	}
	rotated, err := Rotate(first.EncryptedPrivate, "Git Messages Security", "security@example.test", secret, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("rotate PGP keypair: %v", err)
	}
	if rotated.Metadata.Fingerprint == first.Metadata.Fingerprint {
		t.Fatal("rotation reused the previous fingerprint")
	}
	if err := VerifyPublicKeyCertification(rotated.Public, first.Public); err != nil {
		t.Fatalf("verify previous-key certification: %v", err)
	}
	if _, err := ParseArmoredPrivate(private); err != nil {
		t.Fatalf("unwrapped private key is invalid: %v", err)
	}
}

func TestStagedKeyRecoveryUsesCommittedFingerprint(t *testing.T) {
	secret := bytes.Repeat([]byte{0x53}, 32)
	now := time.Now().UTC().Truncate(time.Second)
	first, err := Generate("Git Messages Security", "security@example.test", secret, now)
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := Rotate(first.EncryptedPrivate, "Git Messages Security", "security@example.test", secret, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err := WriteCurrent(directory, first); err != nil {
		t.Fatalf("write initial keypair: %v", err)
	}
	if err := WriteStaged(directory, rotated); err != nil {
		t.Fatalf("stage rotated keypair: %v", err)
	}
	if recovered, err := RecoverStaged(directory, first.Metadata.Fingerprint, secret); err != nil || recovered {
		t.Fatalf("uncommitted staged pair recovery = (%v, %v), want (false, nil)", recovered, err)
	}
	if _, err := os.Stat(filepath.Join(directory, "pgp.pub.asc.new")); !os.IsNotExist(err) {
		t.Fatalf("uncommitted staged public key remains, stat error = %v", err)
	}
	if err := WriteStaged(directory, rotated); err != nil {
		t.Fatalf("restage rotated keypair: %v", err)
	}
	if recovered, err := RecoverStaged(directory, rotated.Metadata.Fingerprint, secret); err != nil || !recovered {
		t.Fatalf("committed staged pair recovery = (%v, %v), want (true, nil)", recovered, err)
	}
	activePublic, err := os.ReadFile(filepath.Join(directory, "pgp.pub.asc"))
	if err != nil {
		t.Fatal(err)
	}
	activeMetadata, err := MetadataForPublic(activePublic)
	if err != nil || activeMetadata.Fingerprint != rotated.Metadata.Fingerprint {
		t.Fatalf("active key metadata = %+v, err=%v", activeMetadata, err)
	}
	for _, name := range []string{"pgp.priv.asc.enc", "pgp.priv.old.asc.enc"} {
		info, err := os.Stat(filepath.Join(directory, name))
		if err != nil {
			t.Fatalf("expected %s after recovery: %v", name, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s permissions = %04o, want 0600", name, info.Mode().Perm())
		}
	}
}

func TestRejectsMalformedKeysAndUnsafeFiles(t *testing.T) {
	secret := bytes.Repeat([]byte{0x61}, 32)
	if _, err := Generate("", "security@example.test", secret, time.Now()); err == nil {
		t.Fatal("empty identity name was accepted")
	}
	if _, err := Generate("Project Security", "", secret, time.Now()); err == nil {
		t.Fatal("empty identity email was accepted")
	}
	if _, err := Generate("Project Security", "security@example.test", []byte("short"), time.Now()); err == nil {
		t.Fatal("short installation secret was accepted")
	}
	if _, err := MetadataForPublic([]byte("not a public key")); err == nil {
		t.Fatal("invalid public key was accepted")
	}
	if _, err := EncryptPrivate([]byte("private key"), []byte("short")); err == nil {
		t.Fatal("short installation secret was accepted for private-key encryption")
	}
	if _, err := Import([]byte("not a private key"), secret); err == nil {
		t.Fatal("invalid private key was imported")
	}
	if _, err := Encrypt([]byte("not a public key"), []byte("message")); err == nil {
		t.Fatal("invalid public key was used for encryption")
	}
	if _, err := Decrypt([]byte("not a message"), []byte("not a private key"), secret); err == nil {
		t.Fatal("invalid keys were accepted for decryption")
	}
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "not-a-file"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := readRegularFile(filepath.Join(directory, "not-a-file"), 1024); err == nil {
		t.Fatal("directory was read as a regular file")
	}
	if err := os.WriteFile(filepath.Join(directory, "target"), []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(directory, "target"), filepath.Join(directory, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := readRegularFile(filepath.Join(directory, "link"), 1024); err == nil {
		t.Fatal("symlink was read as a regular file")
	}
	if _, err := readRegularFile(filepath.Join(directory, "target"), 2); err == nil {
		t.Fatal("oversized file was read")
	}
	if _, err := readRegularFile(directory, 1024); err == nil {
		t.Fatal("directory was read as a file")
	}
}

func TestCurrentKeyRecoveryAndStagedDiscard(t *testing.T) {
	secret := bytes.Repeat([]byte{0x72}, 32)
	pair, err := Generate("Project Security", "security@example.test", secret, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err := WriteCurrent(directory, pair); err != nil {
		t.Fatal(err)
	}
	if err := WriteCurrent(directory, pair); err == nil {
		t.Fatal("existing current keypair was overwritten")
	}
	recovered, err := RecoverStaged(directory, pair.Metadata.Fingerprint, secret)
	if err != nil || !recovered {
		t.Fatalf("active keypair validation = (%v, %v), want (true, nil)", recovered, err)
	}
	if err := DiscardStaged(directory); err != nil {
		t.Fatal(err)
	}
	if err := DiscardStaged(filepath.Join(directory, "missing")); err != nil {
		t.Fatalf("discarding an absent stage should be idempotent: %v", err)
	}
}
