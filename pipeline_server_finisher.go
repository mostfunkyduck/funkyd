package main

type PipelineFinisher struct {
	pipelineServerWorker

	// dedicated channel for servfails, this makes
	// the code more readable than just overriding one of the other channels
	servfailsChannel chan Query
}
