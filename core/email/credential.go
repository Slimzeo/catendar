package email

import (
	"errors"
	"fmt"

	keyring "github.com/zalando/go-keyring"
)

const (
	keyringService       = "catendar"
	legacyKeyringService = "CATendar"
)

type CredentialStore interface {
	Set(accountID, secret string) error
	Get(accountID string) (string, error)
	Exists(accountID string) (bool, error)
}

type KeyringCredentials struct{}

func NewKeyringCredentials() KeyringCredentials {
	return KeyringCredentials{}
}

func (KeyringCredentials) Set(accountID, secret string) error {
	if err := keyring.Set(keyringService, credentialKey(accountID), secret); err != nil {
		return fmt.Errorf("save email credential in system keychain: %w", err)
	}
	return nil
}

func (KeyringCredentials) Get(accountID string) (string, error) {
	secret, err := lookupCredential(accountID)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", errors.New("email credential is not configured")
	}
	if err != nil {
		return "", fmt.Errorf("read email credential from system keychain: %w", err)
	}
	return secret, nil
}

func (KeyringCredentials) Exists(accountID string) (bool, error) {
	_, err := lookupCredential(accountID)
	if errors.Is(err, keyring.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect email credential in system keychain: %w", err)
	}
	return true, nil
}

func lookupCredential(accountID string) (string, error) {
	key := credentialKey(accountID)
	secret, err := keyring.Get(keyringService, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return keyring.Get(legacyKeyringService, key)
	}
	return secret, err
}

func credentialKey(accountID string) string {
	return "email:" + accountID
}
