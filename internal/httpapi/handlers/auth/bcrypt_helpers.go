package auth

import "golang.org/x/crypto/bcrypt"

var passwordHashCost = bcrypt.DefaultCost

func generatePasswordHash(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), passwordHashCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}
