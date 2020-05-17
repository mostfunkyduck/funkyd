package main

// Manages information about upstream servers

import (
	"fmt"
)

type UpstreamName string
type UpstreamWeight float64
type Upstream struct {
	// The hostname of the upstream
	Name UpstreamName

	// The port to connect to
	Port int

	// The current weight score of this upstream
	weight UpstreamWeight
}

func (u *Upstream) GetAddress() string {
	port := u.Port
	if port == 0 {
		port = 853
	}
	return fmt.Sprintf("%s:%d", u.Name, port)
}

func (u *Upstream) GetWeight() UpstreamWeight {
	return u.weight
}

func (u *Upstream) SetWeight(w UpstreamWeight) {
	u.weight = w
}
