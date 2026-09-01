package official

import (
	"coinsphere/backend/plugin/official/internal/safehttp"
	"coinsphere/backend/plugin/sdk"
)

type NetworkClientFactory struct{}

func (NetworkClientFactory) New(allowedHosts []string) (sdk.NetworkClient, error) {
	return safehttp.New(allowedHosts)
}
