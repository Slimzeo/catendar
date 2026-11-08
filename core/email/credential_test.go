package email

import (
	"testing"

	keyring "github.com/zalando/go-keyring"
)

func TestLegacyCredentialAndNewAccountWrites(t *testing.T) {
	keyring.MockInit()
	store := NewKeyringCredentials()
	if ok, err := store.Exists("primary"); err != nil || ok {
		t.Fatalf("empty keychain: exists=%v err=%v", ok, err)
	}
	if err := keyring.Set(legacyKeyringService, "email:primary", "old"); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.Exists("primary"); err != nil || !ok {
		t.Fatalf("legacy keychain: exists=%v err=%v", ok, err)
	}
	if got, err := store.Get("primary"); err != nil || got != "old" {
		t.Fatalf("read legacy entry: got=%q err=%v", got, err)
	}
	if err := store.Set("primary", "updated"); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Get("primary"); err != nil || got != "updated" {
		t.Fatalf("read new entry: got=%q err=%v", got, err)
	}
	if got, err := keyring.Get(keyringService, "email:primary"); err != nil || got != "updated" {
		t.Fatalf("expected updated entry under lowercase service")
	}
}
