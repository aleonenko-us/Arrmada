package main

import (
	"context"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/indexer"
)

// wireUsenet lets the indexers search usenet (Newznab) indexers only while a usenet
// download client exists. HasTransport is cached until a client changes, so asking on
// every search costs nothing, and adding or removing SABnzbd applies to the next search.
func wireUsenet(indexers *indexer.Service, downloads *download.Service) {
	indexers.SetUsenetAvailable(func() bool {
		return downloads.HasTransport(context.Background(), download.TransportUsenet)
	})
}
