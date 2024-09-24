package interfaces

import (
	"fmt"

	"github.com/newrelic/newrelic-client-go/v2/newrelic"
	"github.com/newrelic/newrelic-client-go/v2/pkg/alerts"
	"github.com/newrelic/newrelic-client-go/v2/pkg/config"
	"github.com/newrelic/newrelic-client-go/v2/pkg/notifications"
	"github.com/newrelic/newrelic-client-go/v2/pkg/workflows"
)

// NewRelicClientInterface defines the methods for interacting with the NR API
type NewRelicClientInterface interface {
	Alerts() *alerts.Alerts
	Notifications() *notifications.Notifications
	Workflows() *workflows.Workflows
}

// NewRelicClientWrapper wraps the New Relic client and implements NewRelicClientInterface
type NewRelicClientWrapper struct {
	client *newrelic.NewRelic
}

// Alerts returns the Alerts client
func (n *NewRelicClientWrapper) Alerts() *alerts.Alerts {
	return &n.client.Alerts
}

// Notifications returns the notifications client
func (n *NewRelicClientWrapper) Notifications() *notifications.Notifications {
	return &n.client.Notifications
}

// Workflows returns the Workflows client
func (n *NewRelicClientWrapper) Workflows() *workflows.Workflows {
	return &n.client.Workflows
}

// NewClient initializes a new instance of NR Client
func NewClient(apiKey string, regionVal string) (*newrelic.NewRelic, error) {
	cfg := config.New()

	client, err := newrelic.New(
		newrelic.ConfigPersonalAPIKey(apiKey),
		newrelic.ConfigLogLevel(cfg.LogLevel),
		newrelic.ConfigRegion(regionVal),
	)

	if err != nil {
		return nil, err
	}

	return client, nil
}

// InitNewClient initalizes a given client's CRUD functionality
func InitNewClient(apiKey string, regionName string) (NewRelicClientInterface, error) {
	client, err := NewClient(apiKey, regionName)
	if err != nil {
		return nil, fmt.Errorf("unable to create New Relic client with error: %s", err)
	}

	return &NewRelicClientWrapper{client: client}, nil
}

// PartialAPIKey - Returns a partial API key to ensure we don't log the full API Key
func PartialAPIKey(apiKey string) string {
	partialKeyLength := min(10, len(apiKey))
	return apiKey[0:partialKeyLength] + "..."
}

func min(x, y int) int {
	if x > y {
		return y
	}
	return x
}
