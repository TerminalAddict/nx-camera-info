package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"errors"
	"strings"
	"unicode/utf8"
)

// Nx's published format uses AES-128-CBC, a fixed key/IV, hex encoding and
// zero padding. This is a compatibility implementation using Go's crypto API.
// Format reference (Network Optix, MPL-2.0):
// https://github.com/networkoptix/nx_open/blob/96b23e2661065723711bc00bb7e7cceade3acb0d/libs/nx_utils/src/nx/utils/crypt/symmetrical.cpp
func decodeCredentials(encoded string) (username, password string, err error) {
	if encoded == "" {
		return "", "", nil
	}
	data, err := hex.DecodeString(encoded)
	if err != nil || len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return "", "", errors.New("unsupported credential encoding or invalid ciphertext length")
	}
	key := []byte{0x44, 0x53, 0xd6, 0x65, 0x4c, 0x63, 0x46, 0x36, 0x99, 0x0b, 0x2e, 0x5a, 0xa6, 0x9a, 0x13, 0x12}
	iv := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", "", err
	}
	clear := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(clear, data)
	end := bytes.IndexByte(clear, 0)
	if end < 0 || len(clear)-end > aes.BlockSize {
		return "", "", errors.New("unsupported credential format: invalid zero padding")
	}
	for _, b := range clear[end:] {
		if b != 0 {
			return "", "", errors.New("unsupported credential format: invalid zero padding")
		}
	}
	if !utf8.Valid(clear[:end]) {
		return "", "", errors.New("unsupported credential format: invalid UTF-8")
	}
	// Nx splits at the first colon; a password may itself contain colons.
	username, password, _ = strings.Cut(string(clear[:end]), ":")
	return username, password, nil
}
