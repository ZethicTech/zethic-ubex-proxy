// Package encryption implements the AES-CBC scheme the Flexcube (SANGAM) APIs
// expect: PBKDF2-SHA256 key derivation over a static salt, PKCS#7 padding, and
// a "base64(ciphertext).base64(iv)" wire format.
//
// The scheme must stay byte-compatible with the upstream API - if it ever
// changes, change it here as well and regenerate the test vectors.
package encryption

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

const (
	staticSalt = "1234"
	iterations = 65536
	keyLength  = 32
)

// EncryptAES encrypts plain text and returns base64(ciphertext).base64(iv).
func EncryptAES(plainText, password string) (string, error) {
	key := deriveKey(password)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	// The upstream contract uses a zero IV; it is transmitted alongside the
	// ciphertext regardless.
	iv := make([]byte, aes.BlockSize)
	mode := cipher.NewCBCEncrypter(block, iv)

	padded := pkcs7Pad([]byte(plainText), aes.BlockSize)
	cipherText := make([]byte, len(padded))
	mode.CryptBlocks(cipherText, padded)

	return base64.StdEncoding.EncodeToString(cipherText) + "." + base64.StdEncoding.EncodeToString(iv), nil
}

// DecryptAES reverses EncryptAES, taking the IV from the payload itself.
func DecryptAES(encryptedText, password string) (string, error) {
	encryptedText = strings.Trim(encryptedText, `"`)

	parts := strings.Split(encryptedText, ".")
	if len(parts) != 2 {
		return "", errors.New("invalid format")
	}

	cipherText, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		return "", err
	}
	iv, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", err
	}
	if len(iv) != aes.BlockSize {
		return "", errors.New("invalid IV length")
	}
	if len(cipherText) == 0 || len(cipherText)%aes.BlockSize != 0 {
		return "", errors.New("ciphertext is not a multiple of the block size")
	}

	block, err := aes.NewCipher(deriveKey(password))
	if err != nil {
		return "", err
	}

	mode := cipher.NewCBCDecrypter(block, iv)
	decrypted := make([]byte, len(cipherText))
	mode.CryptBlocks(decrypted, cipherText)

	return pkcs7Unpad(decrypted, aes.BlockSize)
}

func deriveKey(password string) []byte {
	return pbkdf2.Key([]byte(password), []byte(staticSalt), iterations, keyLength, sha256.New)
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padLen := blockSize - len(data)%blockSize
	return append(data, bytes.Repeat([]byte{byte(padLen)}, padLen)...)
}

func pkcs7Unpad(data []byte, blockSize int) (string, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return "", errors.New("invalid padding size")
	}
	padLen := int(data[len(data)-1])
	if padLen == 0 || padLen > blockSize {
		return "", errors.New("invalid padding")
	}
	for _, v := range data[len(data)-padLen:] {
		if int(v) != padLen {
			return "", errors.New("invalid padding")
		}
	}
	return string(data[:len(data)-padLen]), nil
}
