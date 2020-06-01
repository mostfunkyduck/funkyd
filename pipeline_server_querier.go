package main

// PipelineQuerier handles making and processing queries and replies.

import (
	"fmt"

	"github.com/miekg/dns"
)

type PipelineQuerier struct {
	pipelineServerWorker

	// client to send outbound queries with
	client Client

	// send messages here to be cached
	cachingChannel chan Query
}

func (q *PipelineQuerier) Query(qu Query) (query Query, err error) {
	query = qu
	RecursiveQueryCounter.Inc()

	var reply *dns.Msg
	if reply, err = attemptExchange(qu.Msg, query.Conn, q.client); err != nil {
		Logger.Log(NewLogMessage(
			WARNING,
			LogContext{
				"what": "failed exchange with upstreams", "error": err.Error(),
			},
			nil,
		))
		// this connection is tainted, try another one
		query.ConnectionRetries++
		// bail
		return query, fmt.Errorf("failed to exchange query %s: %s", qu.Msg.String(), err)
	}

	query.Reply = reply.Copy()
	query.Reply.SetReply(qu.Msg)
	return query, nil
}

func (q *PipelineQuerier) Start() {
	go func() {
		for {
			select {
			case <-q.cancelChannel:
				logCancellation("PipelineQuerier")
				return
			case query := <-q.inboundQueryChannel:
				// run the actual queries in grs to get maximum throughput - nothing in there should cause contention
				go func() {
					qu, err := q.Query(query)
					if err != nil {
						Logger.Log(LogMessage{
							Level: ERROR,
							Context: LogContext{
								"what":  "error retrieving record for domain",
								"query": query.Msg.String(),
								"error": err.Error(),
								"next":  "failing query",
							},
						})
						// fail to PipelineFinisher
						q.Fail(query)
						return
					}
					// dispatch to replier
					go q.Dispatch(qu)
					// if this was a noerror query, cache the reply
					if qu.Reply.Rcode == 0 {
						go func() { q.cachingChannel <- qu }()
					}
				}()
			}
		}
	}()
}
