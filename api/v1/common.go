package v1

import (
	"errors"
	"strconv"
	"strings"
)

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

// NormalizeDecimal collapses "99", "99.0", "99.00000", "9.9e1" to one canonical
// form ("99"), so decimal-as-string spec fields compare equal regardless of how
// the user wrote them. Unparseable input is returned untouched - the webhook
// owns the error message for that case, not this helper.
func NormalizeDecimal(value string) string {
	f, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return value
	}

	return strconv.FormatFloat(f, 'f', -1, 64)
}

// NormalizeNRQL strips surrounding whitespace. TrimSpace ONLY.
// Do NOT use strings.Join(strings.Fields(s), " ") - it collapses whitespace
// inside quoted literals ('my  service' -> 'my service'), making two different
// specs compare equal and suppressing a needed update. TrimSpace can only cause
// a spurious update, never a missed one.
func NormalizeNRQL(clause string) string {
	return strings.TrimSpace(clause)
}
