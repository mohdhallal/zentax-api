package usecases

import (
	"context"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/mohamadhallal/zentax-api/modules/auth/domain"
)

type InternalAuth struct {
	repo domain.InternalAPIKeyRepo
}

func NewInternalAuth(repo domain.InternalAPIKeyRepo) *InternalAuth {
	return &InternalAuth{repo: repo}
}

func (uc *InternalAuth) Validate(ctx context.Context, key, secret string) (*domain.AuthResult, error) {
	keyUUID, err := uuid.Parse(key)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", domain.ErrMsgInvalidKeyFormat, err)
	}

	apiKey, err := uc.repo.GetByKey(ctx, keyUUID)
	if err != nil {
		return nil, err
	}
	if apiKey == nil {
		return nil, errors.New(domain.ErrMsgInvalidCredentials)
	}

	if !compareSecretHash(secret, apiKey.SecretHash) {
		return nil, errors.New(domain.ErrMsgInvalidCredentials)
	}

	return &domain.AuthResult{
		ServiceName: apiKey.AppName,
	}, nil
}

func compareSecretHash(secret, storedHash string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(secret)
	if err != nil {
		return false
	}
	hash := sha512.Sum512(raw)
	computedHash := base64.RawURLEncoding.EncodeToString(hash[:])
	return subtle.ConstantTimeCompare([]byte(computedHash), []byte(storedHash)) == 1
}
