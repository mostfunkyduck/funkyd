package main

type BaseServer struct {
	// lookup cache
	Cache Cache

	// cache of records hosted by this server
	HostedCache Cache

	// client for recursive lookups
	dnsClient Client
}
