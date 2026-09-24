package security

import (
	"strings"
	"testing"
)

func TestHashPasswordRoundTrip(t *testing.T) {
	const plain = "correct horse battery staple"
	h, err := HashPassword(plain)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(h, "$argon2id$") {
		t.Fatalf("hash %q missing PHC prefix", h)
	}
	ok, err := VerifyPassword(h, plain)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ok {
		t.Fatal("verify rejected correct password")
	}
}

func TestVerifyPasswordRejectsWrong(t *testing.T) {
	h, err := HashPassword("right")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	ok, err := VerifyPassword(h, "wrong")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if ok {
		t.Fatal("verify accepted wrong password")
	}
}

func TestHashPasswordEmpty(t *testing.T) {
	if _, err := HashPassword(""); err != ErrPasswordEmpty {
		t.Fatalf("want ErrPasswordEmpty, got %v", err)
	}
}

func TestVerifyPasswordMalformed(t *testing.T) {
	cases := []struct {
		name string
		hash string
	}{
		{"empty", ""},
		{"wrong algo", "$bcrypt$v=19$m=1,t=1,p=1$AAAA$BBBB"},
		{"too few parts", "$argon2id$v=19$m=1,t=1,p=1$AAAA"},
		{"bad params", "$argon2id$v=19$not,a,list$AAAA$BBBB"},
		{"bad salt b64", "$argon2id$v=19$m=1,t=1,p=1$!!!notbase64$BBBB"},
		{"bad hash b64", "$argon2id$v=19$m=1,t=1,p=1$AAAA$!!!notbase64"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, err := VerifyPassword(c.hash, "anything")
			if err == nil {
				t.Fatal("want error for malformed hash")
			}
			if ok {
				t.Fatal("malformed hash should not verify")
			}
		})
	}
}

func TestHashOutputIsUniquePerCall(t *testing.T) {
	h1, _ := HashPassword("same")
	h2, _ := HashPassword("same")
	if h1 == h2 {
		t.Fatal("two hashes of the same password should differ (random salt)")
	}
}