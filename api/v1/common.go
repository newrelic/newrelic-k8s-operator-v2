package v1

import (
	"errors"
	"strconv"
	"strings"
	"sync/atomic"

	"k8s.io/apimachinery/pkg/util/validation/field"
)

// defaultAccountID is the operator-level fallback for spec.accountId. It is set
// once at startup (see cmd/main.go) and read by the defaulting webhooks, which
// run on freshly decoded objects and so cannot carry config on a receiver.
var defaultAccountID atomic.Int64

// SetDefaultAccountID configures the account ID applied to CRs that omit
// spec.accountId. 0 (or negative) disables the default.
func SetDefaultAccountID(id int) {
	if id < 0 {
		id = 0
	}
	defaultAccountID.Store(int64(id))
}

// DefaultAccountID returns the configured operator default account ID, or 0 if none.
func DefaultAccountID() int {
	return int(defaultAccountID.Load())
}

// applyDefaultAccountID fills in an unset account ID from the operator default.
// An explicit value is never overwritten.
func applyDefaultAccountID(accountID *int) {
	if *accountID == 0 && DefaultAccountID() > 0 {
		*accountID = DefaultAccountID()
	}
}

// accountIDRequiredError is the validation error for a missing spec.accountId.
func accountIDRequiredError() *field.Error {
	return field.Required(field.NewPath("spec").Child("accountId"),
		"accountId must be set as type integer, or configure an operator default via "+
			"--default-account-id / NEW_RELIC_ACCOUNT_ID (Helm: controllerManager.manager.defaultAccountId)")
}

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
