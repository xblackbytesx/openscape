package auth

import (
	"fmt"
	"os"
	"strconv"

	"golang.org/x/crypto/bcrypt"
)

// bcryptCost defaults to 12 (a 2026-reasonable cost on modest CPUs). Override
// via BCRYPT_COST env var to bump it as hardware improves; clamped to bcrypt's
// own [4, 31] range, with anything < 10 rejected so a typo doesn't accidentally
// downgrade existing deploys.
var bcryptCost = loadBcryptCost()

func loadBcryptCost() int {
	v := os.Getenv("BCRYPT_COST")
	if v == "" {
		return 12
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 10 || n > bcrypt.MaxCost {
		return 12
	}
	return n
}

func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
