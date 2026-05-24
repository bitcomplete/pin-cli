package main

import (
	"crypto/rand"
	"crypto/sha256"

	"github.com/zalando/go-keyring"
)

// Single "service" name in the OS keychain; each issuer (PIN_HOST) gets
// its own row so multi-environment users (dev vs prod) don't collide.
const keyringService = "pin"

func keychainSet(issuer, value string) error {
	return keyring.Set(keyringService, issuer, value)
}

func keychainGet(issuer string) (string, error) {
	return keyring.Get(keyringService, issuer)
}

func keychainDel(issuer string) error {
	return keyring.Delete(keyringService, issuer)
}

// ----- pure crypto helpers (kept here to avoid pulling x/crypto in main.go) -----

func sha256Sum(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

func readRandom(b []byte) (int, error) {
	return rand.Read(b)
}
