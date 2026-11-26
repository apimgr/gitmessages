package pgp

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

const (
	privateEnvelopeMagic = "GMPGP1"
	privateSaltSize      = 16
	privateNonceSize     = 12
	keyLifetimeYears     = 2
)

var (
	errInvalidPrivateEnvelope = errors.New("invalid encrypted PGP private key")
	errInvalidInstallationKey = errors.New("installation secret must contain at least 32 bytes")
)

// Metadata describes the public properties of an OpenPGP identity.
type Metadata struct {
	Fingerprint string
	Identity    string
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// KeyPair contains the armored public key and an installation-secret-wrapped private key.
type KeyPair struct {
	Public           []byte
	EncryptedPrivate []byte
	Metadata         Metadata
}

// Generate creates an Ed25519 signing key with a Curve25519 encryption subkey.
func Generate(name, email string, installationSecret []byte, now time.Time) (KeyPair, error) {
	if len(installationSecret) != 32 {
		return KeyPair{}, errInvalidInstallationKey
	}
	if strings.TrimSpace(name) == "" || strings.TrimSpace(email) == "" || strings.ContainsAny(name, "()<>\x00") || strings.ContainsAny(email, "()<>\x00") {
		return KeyPair{}, errors.New("invalid PGP identity")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC().Truncate(time.Second)
	expires := now.AddDate(keyLifetimeYears, 0, 0)
	lifetime := uint32(expires.Sub(now).Seconds())
	configuration := &packet.Config{
		Algorithm:       packet.PubKeyAlgoEd25519,
		KeyLifetimeSecs: lifetime,
		Time:            func() time.Time { return now },
	}
	entity, err := openpgp.NewEntity(name, "", email, configuration)
	if err != nil {
		return KeyPair{}, fmt.Errorf("generate PGP keypair: %w", err)
	}
	return serializeEntity(entity, installationSecret, configuration)
}

// Rotate creates a new key and certifies its identity using the current private key.
func Rotate(currentPrivate []byte, name, email string, installationSecret []byte, now time.Time) (KeyPair, error) {
	oldEntity, err := ParsePrivate(currentPrivate, installationSecret)
	if err != nil {
		return KeyPair{}, fmt.Errorf("load current PGP key: %w", err)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC().Truncate(time.Second)
	expires := now.AddDate(keyLifetimeYears, 0, 0)
	configuration := &packet.Config{
		Algorithm:       packet.PubKeyAlgoEd25519,
		KeyLifetimeSecs: uint32(expires.Sub(now).Seconds()),
		Time:            func() time.Time { return now },
	}
	entity, err := openpgp.NewEntity(name, "", email, configuration)
	if err != nil {
		return KeyPair{}, fmt.Errorf("generate replacement PGP keypair: %w", err)
	}
	for identity := range entity.Identities {
		if err := entity.SignIdentity(identity, oldEntity, configuration); err != nil {
			return KeyPair{}, fmt.Errorf("certify replacement PGP identity: %w", err)
		}
	}
	pair, err := serializeEntity(entity, installationSecret, configuration)
	if err != nil {
		return KeyPair{}, err
	}
	return pair, nil
}

func serializeEntity(entity *openpgp.Entity, installationSecret []byte, configuration *packet.Config) (KeyPair, error) {
	var public bytes.Buffer
	publicArmor, err := armor.Encode(&public, openpgp.PublicKeyType, nil)
	if err != nil {
		return KeyPair{}, fmt.Errorf("armor PGP public key: %w", err)
	}
	if err := entity.Serialize(publicArmor); err != nil {
		_ = publicArmor.Close()
		return KeyPair{}, fmt.Errorf("serialize PGP public key: %w", err)
	}
	if err := publicArmor.Close(); err != nil {
		return KeyPair{}, fmt.Errorf("finish PGP public key: %w", err)
	}
	var private bytes.Buffer
	privateArmor, err := armor.Encode(&private, openpgp.PrivateKeyType, nil)
	if err != nil {
		return KeyPair{}, fmt.Errorf("armor PGP private key: %w", err)
	}
	if err := entity.SerializePrivate(privateArmor, configuration); err != nil {
		_ = privateArmor.Close()
		return KeyPair{}, fmt.Errorf("serialize PGP private key: %w", err)
	}
	if err := privateArmor.Close(); err != nil {
		return KeyPair{}, fmt.Errorf("finish PGP private key: %w", err)
	}
	encrypted, err := EncryptPrivate(private.Bytes(), installationSecret)
	if err != nil {
		return KeyPair{}, err
	}
	metadata, err := MetadataForPublic(public.Bytes())
	if err != nil {
		return KeyPair{}, err
	}
	return KeyPair{Public: public.Bytes(), EncryptedPrivate: encrypted, Metadata: metadata}, nil
}

// MetadataForPublic validates the algorithm and returns the primary fingerprint and lifetime.
func MetadataForPublic(public []byte) (Metadata, error) {
	entities, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(public))
	if err != nil || len(entities) != 1 {
		return Metadata{}, errors.New("invalid armored PGP public key")
	}
	entity := entities[0]
	if entity.PrimaryKey == nil || entity.PrimaryKey.PubKeyAlgo != packet.PubKeyAlgoEd25519 {
		return Metadata{}, errors.New("PGP primary key must use Ed25519")
	}
	hasEncryptionSubkey := false
	for _, subkey := range entity.Subkeys {
		if subkey.PublicKey != nil && subkey.PublicKey.PubKeyAlgo == packet.PubKeyAlgoX25519 {
			hasEncryptionSubkey = true
			break
		}
	}
	if !hasEncryptionSubkey {
		return Metadata{}, errors.New("PGP key is missing a Curve25519 encryption subkey")
	}
	metadata := Metadata{Fingerprint: strings.ToUpper(hex.EncodeToString(entity.PrimaryKey.Fingerprint)), CreatedAt: entity.PrimaryKey.CreationTime.UTC()}
	for _, identity := range entity.Identities {
		if identity.SelfSignature != nil && identity.SelfSignature.IsPrimaryId != nil && *identity.SelfSignature.IsPrimaryId {
			metadata.Identity = identity.UserId.Id
			break
		}
	}
	if metadata.Identity == "" {
		for _, identity := range entity.Identities {
			metadata.Identity = identity.UserId.Id
			break
		}
	}
	var lifetime *uint32
	if entity.SelfSignature != nil {
		lifetime = entity.SelfSignature.KeyLifetimeSecs
	}
	if lifetime == nil {
		for _, identity := range entity.Identities {
			if identity.SelfSignature != nil && identity.SelfSignature.KeyLifetimeSecs != nil {
				lifetime = identity.SelfSignature.KeyLifetimeSecs
				break
			}
		}
	}
	if lifetime != nil && *lifetime > 0 {
		metadata.ExpiresAt = metadata.CreatedAt.Add(time.Duration(*lifetime) * time.Second)
	}
	return metadata, nil
}

// EncryptPrivate wraps armored private-key material with an AES-256-GCM key derived from the installation secret.
func EncryptPrivate(private, installationSecret []byte) ([]byte, error) {
	if len(installationSecret) != 32 {
		return nil, errInvalidInstallationKey
	}
	key, salt, err := derivePrivateKey(installationSecret)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initialize PGP private-key encryption: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize PGP private-key authentication: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate PGP private-key nonce: %w", err)
	}
	result := make([]byte, 0, len(privateEnvelopeMagic)+len(salt)+len(nonce)+len(private)+gcm.Overhead())
	result = append(result, privateEnvelopeMagic...)
	result = append(result, salt...)
	result = append(result, nonce...)
	result = gcm.Seal(result, nonce, private, []byte(privateEnvelopeMagic))
	return result, nil
}

// DecryptPrivate unwraps the AES-256-GCM protected private-key material.
func DecryptPrivate(encrypted, installationSecret []byte) ([]byte, error) {
	if len(installationSecret) != 32 {
		return nil, errInvalidInstallationKey
	}
	minimum := len(privateEnvelopeMagic) + privateSaltSize + privateNonceSize + 16
	if len(encrypted) < minimum || string(encrypted[:len(privateEnvelopeMagic)]) != privateEnvelopeMagic {
		return nil, errInvalidPrivateEnvelope
	}
	start := len(privateEnvelopeMagic)
	salt := encrypted[start : start+privateSaltSize]
	start += privateSaltSize
	nonce := encrypted[start : start+privateNonceSize]
	start += privateNonceSize
	key := derivePrivateKeyWithSalt(installationSecret, salt)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errInvalidPrivateEnvelope
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(nonce) != gcm.NonceSize() {
		return nil, errInvalidPrivateEnvelope
	}
	private, err := gcm.Open(nil, nonce, encrypted[start:], []byte(privateEnvelopeMagic))
	if err != nil {
		return nil, errInvalidPrivateEnvelope
	}
	return private, nil
}

// RewrapPrivateKey changes the installation-secret wrapping without changing the PGP key.
func RewrapPrivateKey(encrypted, oldInstallationSecret, newInstallationSecret []byte) ([]byte, error) {
	private, err := DecryptPrivate(encrypted, oldInstallationSecret)
	if err != nil {
		return nil, err
	}
	if _, err := ParseArmoredPrivate(private); err != nil {
		return nil, err
	}
	return EncryptPrivate(private, newInstallationSecret)
}

// ExportPrivate returns validated ASCII-armored private-key bytes for a separately authorized operator flow.
func ExportPrivate(encrypted, installationSecret []byte) ([]byte, error) {
	private, err := DecryptPrivate(encrypted, installationSecret)
	if err != nil {
		return nil, err
	}
	if _, err := ParseArmoredPrivate(private); err != nil {
		return nil, err
	}
	return private, nil
}

func derivePrivateKey(secret []byte) ([]byte, []byte, error) {
	salt := make([]byte, privateSaltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, nil, fmt.Errorf("generate PGP private-key salt: %w", err)
	}
	return derivePrivateKeyWithSalt(secret, salt), salt, nil
}

func derivePrivateKeyWithSalt(secret, salt []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("gitmessages/pgp-private-key/v1\x00"))
	_, _ = mac.Write(salt)
	return mac.Sum(nil)
}

// ParsePrivate decrypts and validates an installation-secret-wrapped private key.
func ParsePrivate(encrypted, installationSecret []byte) (*openpgp.Entity, error) {
	private, err := DecryptPrivate(encrypted, installationSecret)
	if err != nil {
		return nil, err
	}
	return ParseArmoredPrivate(private)
}

// ParseArmoredPrivate validates an unwrapped armored private key.
func ParseArmoredPrivate(private []byte) (*openpgp.Entity, error) {
	entities, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(private))
	if err != nil || len(entities) != 1 || entities[0].PrivateKey == nil {
		return nil, errors.New("invalid armored PGP private key")
	}
	if entities[0].PrivateKey.Encrypted {
		return nil, errors.New("imported PGP private key must be decrypted before installation-secret wrapping")
	}
	for _, subkey := range entities[0].Subkeys {
		if subkey.PrivateKey != nil && subkey.PrivateKey.Encrypted {
			return nil, errors.New("imported PGP private key must be decrypted before installation-secret wrapping")
		}
	}
	if _, err := MetadataForEntity(entities[0]); err != nil {
		return nil, fmt.Errorf("validate PGP private key: %w", err)
	}
	return entities[0], nil
}

// Import validates an unwrapped private key and returns a keypair ready for persistence.
func Import(private, installationSecret []byte) (KeyPair, error) {
	if len(installationSecret) != 32 {
		return KeyPair{}, errInvalidInstallationKey
	}
	entity, err := ParseArmoredPrivate(private)
	if err != nil {
		return KeyPair{}, err
	}
	var public bytes.Buffer
	writer, err := armor.Encode(&public, openpgp.PublicKeyType, nil)
	if err != nil {
		return KeyPair{}, err
	}
	if err := entity.Serialize(writer); err != nil {
		_ = writer.Close()
		return KeyPair{}, err
	}
	if err := writer.Close(); err != nil {
		return KeyPair{}, err
	}
	metadata, err := MetadataForPublic(public.Bytes())
	if err != nil {
		return KeyPair{}, err
	}
	encrypted, err := EncryptPrivate(private, installationSecret)
	if err != nil {
		return KeyPair{}, err
	}
	return KeyPair{Public: public.Bytes(), EncryptedPrivate: encrypted, Metadata: metadata}, nil
}

// MetadataForEntity returns metadata for an already parsed entity.
func MetadataForEntity(entity *openpgp.Entity) (Metadata, error) {
	if entity == nil || entity.PrimaryKey == nil {
		return Metadata{}, errors.New("invalid PGP entity")
	}
	var public bytes.Buffer
	writer, err := armor.Encode(&public, openpgp.PublicKeyType, nil)
	if err != nil {
		return Metadata{}, err
	}
	if err := entity.Serialize(writer); err != nil {
		_ = writer.Close()
		return Metadata{}, err
	}
	if err := writer.Close(); err != nil {
		return Metadata{}, err
	}
	return MetadataForPublic(public.Bytes())
}

// CertifyPublicKey verifies that oldEntity certifies an identity on newPublic.
func CertifyPublicKey(newPublic []byte, oldEntity *openpgp.Entity) ([]byte, error) {
	if oldEntity == nil || oldEntity.PrivateKey == nil || oldEntity.PrivateKey.Encrypted {
		return nil, errors.New("current PGP private key is unavailable for certification")
	}
	entities, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(newPublic))
	if err != nil || len(entities) != 1 {
		return nil, errors.New("invalid replacement PGP public key")
	}
	entity := entities[0]
	for identity := range entity.Identities {
		if err := entity.SignIdentity(identity, oldEntity, nil); err != nil {
			return nil, fmt.Errorf("sign replacement PGP identity: %w", err)
		}
	}
	var output bytes.Buffer
	writer, err := armor.Encode(&output, openpgp.PublicKeyType, nil)
	if err != nil {
		return nil, err
	}
	if err := entity.Serialize(writer); err != nil {
		_ = writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

// VerifyPublicKeyCertification checks the previous key's certification on a rotated public key.
func VerifyPublicKeyCertification(newPublic []byte, oldPublic []byte) error {
	oldKeys, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(oldPublic))
	if err != nil || len(oldKeys) != 1 {
		return errors.New("invalid previous PGP public key")
	}
	newKeys, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(newPublic))
	if err != nil || len(newKeys) != 1 {
		return errors.New("invalid replacement PGP public key")
	}
	old := oldKeys[0].PrimaryKey
	for _, identity := range newKeys[0].Identities {
		for _, signature := range identity.Signatures {
			if signature.CheckKeyIdOrFingerprint(old) && old.VerifyUserIdSignature(identity.UserId.Id, newKeys[0].PrimaryKey, signature) == nil {
				return nil
			}
		}
	}
	return errors.New("replacement PGP public key is not certified by the previous key")
}

// Encrypt encrypts plaintext to the first usable Curve25519-capable public entity.
func Encrypt(publicKey, plaintext []byte) ([]byte, error) {
	entities, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(publicKey))
	if err != nil || len(entities) == 0 {
		return nil, errors.New("invalid PGP public key")
	}
	var encrypted bytes.Buffer
	writer, err := openpgp.Encrypt(&encrypted, entities, nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("encrypt PGP message: %w", err)
	}
	if _, err := writer.Write(plaintext); err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("write PGP message: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("finish PGP message: %w", err)
	}
	return encrypted.Bytes(), nil
}

// Decrypt decrypts a message using the installation-secret-wrapped private key.
func Decrypt(encrypted, privateKey, installationSecret []byte) ([]byte, error) {
	entity, err := ParsePrivate(privateKey, installationSecret)
	if err != nil {
		return nil, err
	}
	message, err := openpgp.ReadMessage(bytes.NewReader(encrypted), openpgp.EntityList{entity}, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("read PGP message: %w", err)
	}
	plaintext, err := io.ReadAll(message.UnverifiedBody)
	if err != nil {
		return nil, fmt.Errorf("decrypt PGP message: %w", err)
	}
	if message.SignatureError != nil {
		return nil, fmt.Errorf("verify PGP message signature: %w", message.SignatureError)
	}
	return plaintext, nil
}

// WriteStaged writes the new public and wrapped private key files before a database commit.
func WriteStaged(directory string, pair KeyPair) error {
	if err := validatePair(pair); err != nil {
		return err
	}
	if err := secureDirectory(directory); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(directory, "pgp.pub.asc.new"), pair.Public, 0o600); err != nil {
		return fmt.Errorf("stage PGP public key: %w", err)
	}
	if err := writeAtomic(filepath.Join(directory, "pgp.priv.asc.enc.new"), pair.EncryptedPrivate, 0o600); err != nil {
		_ = os.Remove(filepath.Join(directory, "pgp.pub.asc.new"))
		_ = os.Remove(filepath.Join(directory, "pgp.priv.asc.enc.new"))
		return fmt.Errorf("stage PGP private key: %w", err)
	}
	return nil
}

// WriteCurrent writes a generated keypair atomically for first-time installation.
func WriteCurrent(directory string, pair KeyPair) error {
	if err := validatePair(pair); err != nil {
		return err
	}
	if err := secureDirectory(directory); err != nil {
		return err
	}
	publicPath := filepath.Join(directory, "pgp.pub.asc")
	privatePath := filepath.Join(directory, "pgp.priv.asc.enc")
	for _, path := range []string{publicPath, privatePath} {
		if _, err := os.Lstat(path); err == nil {
			return errors.New("PGP keypair already exists")
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect PGP key file: %w", err)
		}
	}
	if err := writeAtomic(privatePath, pair.EncryptedPrivate, 0o600); err != nil {
		return fmt.Errorf("write PGP private key: %w", err)
	}
	if err := writeAtomic(publicPath, pair.Public, 0o600); err != nil {
		_ = os.Remove(privatePath)
		return fmt.Errorf("write PGP public key: %w", err)
	}
	return nil
}

// RecoverStaged finalizes a committed staged pair or removes a pair whose fingerprint was not committed.
func RecoverStaged(directory, committedFingerprint string, installationSecret []byte) (bool, error) {
	publicStage := filepath.Join(directory, "pgp.pub.asc.new")
	privateStage := filepath.Join(directory, "pgp.priv.asc.enc.new")
	public, publicErr := readRegularFile(publicStage, 16<<20)
	if errors.Is(publicErr, os.ErrNotExist) {
		_ = os.Remove(privateStage)
		active, err := readRegularFile(filepath.Join(directory, "pgp.pub.asc"), 16<<20)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("read active PGP public key: %w", err)
		}
		metadata, metadataErr := MetadataForPublic(active)
		if metadataErr != nil || !strings.EqualFold(metadata.Fingerprint, committedFingerprint) {
			return false, errors.New("active PGP public key does not match the committed fingerprint")
		}
		activePrivate, err := readRegularFile(filepath.Join(directory, "pgp.priv.asc.enc"), 16<<20)
		if err != nil {
			return false, fmt.Errorf("read active PGP private key: %w", err)
		}
		entity, err := ParsePrivate(activePrivate, installationSecret)
		if err != nil {
			return false, fmt.Errorf("verify active PGP private key: %w", err)
		}
		privateMetadata, err := MetadataForEntity(entity)
		if err != nil || !strings.EqualFold(privateMetadata.Fingerprint, committedFingerprint) {
			return false, errors.New("active PGP public and private keys do not match the committed fingerprint")
		}
		return true, nil
	}
	if publicErr != nil {
		return false, fmt.Errorf("read staged PGP public key: %w", publicErr)
	}
	metadata, err := MetadataForPublic(public)
	if err != nil || !strings.EqualFold(metadata.Fingerprint, committedFingerprint) {
		if err := DiscardStaged(directory); err != nil {
			return false, fmt.Errorf("remove uncommitted staged PGP key: %w", err)
		}
		return false, nil
	}
	if err := secureDirectory(directory); err != nil {
		return false, err
	}
	private, privateErr := readRegularFile(privateStage, 16<<20)
	if errors.Is(privateErr, os.ErrNotExist) {
		private, privateErr = readRegularFile(filepath.Join(directory, "pgp.priv.asc.enc"), 16<<20)
	}
	if privateErr != nil {
		return false, fmt.Errorf("read staged PGP private key: %w", privateErr)
	}
	entity, err := ParsePrivate(private, installationSecret)
	if err != nil {
		return false, fmt.Errorf("verify staged PGP private key: %w", err)
	}
	privateMetadata, err := MetadataForEntity(entity)
	if err != nil || !strings.EqualFold(privateMetadata.Fingerprint, committedFingerprint) {
		return false, errors.New("staged PGP public and private keys do not match committed fingerprint")
	}
	activePrivate := filepath.Join(directory, "pgp.priv.asc.enc")
	oldPrivate := filepath.Join(directory, "pgp.priv.old.asc.enc")
	if _, err := os.Stat(privateStage); err == nil {
		if _, err := os.Stat(activePrivate); err == nil {
			if _, oldErr := os.Stat(oldPrivate); errors.Is(oldErr, os.ErrNotExist) {
				if err := os.Rename(activePrivate, oldPrivate); err != nil {
					return false, fmt.Errorf("preserve previous PGP private key: %w", err)
				}
			} else if oldErr != nil {
				return false, fmt.Errorf("inspect previous PGP private key: %w", oldErr)
			} else {
				return false, errors.New("previous PGP private key already exists; refusing to overwrite the 30-day key")
			}
		}
		if err := os.Rename(privateStage, activePrivate); err != nil {
			return false, fmt.Errorf("activate staged PGP private key: %w", err)
		}
	}
	if err := os.Rename(publicStage, filepath.Join(directory, "pgp.pub.asc")); err != nil {
		return false, fmt.Errorf("activate staged PGP public key: %w", err)
	}
	if err := syncDirectory(directory); err != nil {
		return false, err
	}
	return true, nil
}

// DiscardStaged removes staged key files from a rolled-back rotation.
func DiscardStaged(directory string) error {
	var errs []error
	for _, name := range []string{"pgp.pub.asc.new", "pgp.priv.asc.enc.new"} {
		if err := os.Remove(filepath.Join(directory, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func validatePair(pair KeyPair) error {
	if len(pair.Public) == 0 || len(pair.EncryptedPrivate) == 0 || pair.Metadata.Fingerprint == "" {
		return errors.New("incomplete PGP keypair")
	}
	metadata, err := MetadataForPublic(pair.Public)
	if err != nil || !strings.EqualFold(metadata.Fingerprint, pair.Metadata.Fingerprint) {
		return errors.New("PGP public key metadata does not match key material")
	}
	return nil
}

func secureDirectory(directory string) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create PGP directory: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("PGP directory must be a real directory")
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("secure PGP directory: %w", err)
	}
	return nil
}

func writeAtomic(destination string, data []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".pgp-stage-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(destination))
}

func syncDirectory(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open PGP directory for sync: %w", err)
	}
	defer func() { _ = file.Close() }()
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync PGP directory: %w", err)
	}
	return nil
}

func readRegularFile(path string, maxBytes int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maxBytes {
		return nil, errors.New("PGP key file must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, errors.New("PGP key file exceeds size limit")
	}
	return data, nil
}
