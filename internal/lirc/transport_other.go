//go:build !linux

package lirc

func discoverPlatform(_ DiscoverOptions) (Transport, error) {
	return NewSimTransport(), nil
}
