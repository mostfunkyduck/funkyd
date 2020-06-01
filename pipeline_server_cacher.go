package main

// PipelineCacher manages and accesses the record cache.

type PipelineCacher struct {
	pipelineServerWorker

	// the actual cache
	cache Cache

	//a channel for inbound queries to be cached
	cachingChannel chan Query
}
