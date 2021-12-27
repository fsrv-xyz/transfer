package main

import "net/http"

// action not possible due to broken backend
func breakIfUnhealthy(w http.ResponseWriter) bool {
	if backendState != StateHealthy {
		w.WriteHeader(http.StatusInternalServerError)
		return true
	}
	return false
}
