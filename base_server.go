package main

type BaseServer struct {
	// lookup cache
	Cache *RecordCache

	// cache of records hosted by this server
	HostedCache *RecordCache

	// client for recursive lookups
	dnsClient Client
}
