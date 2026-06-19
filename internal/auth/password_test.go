package auth

import "testing"

func TestHashAndCheckPassword(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if hash == "correct horse battery staple" {
		t.Fatal("hash equals plaintext — not hashed")
	}
	if err := CheckPassword(hash, "correct horse battery staple"); err != nil {
		t.Errorf("CheckPassword on correct password: %v", err)
	}
	if err := CheckPassword(hash, "wrong password"); err == nil {
		t.Error("CheckPassword accepted a wrong password")
	}
}

func TestHashPasswordIsSalted(t *testing.T) {
	h1, _ := HashPassword("same")
	h2, _ := HashPassword("same")
	if h1 == h2 {
		t.Error("identical passwords produced identical hashes — salt missing")
	}
}
