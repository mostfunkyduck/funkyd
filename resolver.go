package main

import (
	"fmt"
)

func (r *Resolver) GetAddress() string {
	port := r.Port
	if port == 0 {
		port = 853
	}
	return fmt.Sprintf("%s:%d", r.Name, port)
}
