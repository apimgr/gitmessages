package contact

import (
	"bytes"
	"errors"

	"github.com/ProtonMail/go-crypto/openpgp"
)

// EncryptPGP encrypts a payload to every valid public key in an armored keyring.
func EncryptPGP(publicKeys, plaintext []byte) ([]byte, error) {
	entities, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(publicKeys))
	if err != nil || len(entities) == 0 {
		return nil, errors.New("invalid OpenPGP public key")
	}
	var encrypted bytes.Buffer
	writer, err := openpgp.Encrypt(&encrypted, entities, nil, nil, nil)
	if err != nil {
		return nil, err
	}
	if _, err := writer.Write(plaintext); err != nil {
		_ = writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return encrypted.Bytes(), nil
}
