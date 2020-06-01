package main

import (
	"time"
)

// PipelineCacher manages and accesses the record cache.

type PipelineCacher struct {
	pipelineServerWorker

	// the actual cache
	cache Cache

	//a channel for inbound queries to be cached
	cachingChannel chan Query
}

// checks for a query in the cache, the cache object handles
// expiry.
func (c *PipelineCacher) CheckCache(q Query) (result Response, ok bool) {
	if (q.Msg == nil) || len(q.Msg.Question) < 1 {
		return Response{}, false
	}
	return c.cache.Get(q.Msg.Question[0].Name, q.Msg.Question[0].Qtype)
}

// adds a query to the cache.
func (c *PipelineCacher) CacheQuery(q Query) {
	question := q.Msg.Question[0]
	ttl := uint32(0)
	if len(q.Reply.Answer) > 0 {
		ttl = q.Reply.Answer[0].Header().Ttl
	}
	r := Response{
		Ttl:          time.Duration(ttl) * time.Second,
		Entry:        *q.Reply,
		CreationTime: time.Now(),
		Name:         question.Name,
		Qtype:        question.Qtype,
	}
	c.cache.Add(r)
}

func (c *PipelineCacher) Start() {
	go func() {
		for {
			select {
			case q := <-c.inboundQueryChannel:
				if resp, ok := c.CheckCache(q); ok {
					q.Reply = resp.Entry.Copy()
					q.Reply.SetRcode(q.Msg, resp.Entry.Rcode)
					CacheHitsCounter.Inc()
					// pass to PipelineQuerier
					go c.Dispatch(q)
					break
				}
				//	pass to PipelineConnector
				go c.Fail(q)
			case q := <-c.cachingChannel:
				if q.Reply == nil {
					Logger.Log(LogMessage{
						Level: ERROR,
						Context: LogContext{
							"what": "attempted to cache empty reply",
							"next": "discarding cache request",
						},
					})
					break
				}
				c.CacheQuery(q)
				// no need to do anything else
			case <-c.cancelChannel:
				logCancellation("PipelineCacher")
				return
			}
		}
	}()
}
