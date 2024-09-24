package v1

import "errors"

// NewRelicSecret masks sensitive data input into configs
type NewRelicSecret struct {
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	KeyName   string `json:"keyName,omitempty"`
}

// CheckForAPIKeyOrSecret - returns error if a API KEY or k8 secret is not passed in
func CheckForAPIKeyOrSecret(apiKey string, secret NewRelicSecret) error {
	if apiKey != "" {
		return nil
	}

	if secret != (NewRelicSecret{}) {
		if secret.Name != "" && secret.Namespace != "" && secret.KeyName != "" {
			return nil
		}
	}

	return errors.New("either api_key or api_key_secret must be set")
}
