// Package httputil provides shared HTTP response helpers.
package httputil

import (
	"encoding/json"
	"net/http"
)

type Response struct {
	Status string      `json:"status"`
	Data   interface{} `json:"data,omitempty"`
	Error  string      `json:"error,omitempty"`
	Code   string      `json:"code,omitempty"`
}

func RespondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func RespondOK(w http.ResponseWriter, data interface{}) {
	RespondJSON(w, http.StatusOK, data)
}

func RespondCreated(w http.ResponseWriter, data interface{}) {
	RespondJSON(w, http.StatusCreated, data)
}

// ClientIP extracts the real client IP from proxy headers, falling back to RemoteAddr.
func ClientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	if ff := r.Header.Get("X-Forwarded-For"); ff != "" {
		// First entry is the original client
		for i := 0; i < len(ff); i++ {
			if ff[i] == ',' {
				return ff[:i]
			}
		}
		return ff
	}
	return r.RemoteAddr
}

func RespondError(w http.ResponseWriter, status int, code, message string) {
	RespondJSON(w, status, Response{
		Status: "error",
		Error:  message,
		Code:   code,
	})
}
