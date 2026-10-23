package dto

// APIKeySecret is used only by successful create/rotation responses. The embedded
// APIKey keeps a mask, so idempotency storage never receives the credential.
type APIKeySecret struct {
	APIKey
	Key             string `json:"key"`
	SecretAvailable bool   `json:"secret_available"`
}

func (k *APIKeySecret) IdempotencyStoredResponse() any { return k.APIKey }
