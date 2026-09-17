package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

const maxBodyBytes = 16 << 20

type requestMetadata struct {
	CapturedAt      time.Time         `json:"captured_at"`
	Method          string            `json:"method"`
	Path            string            `json:"path"`
	ContentType     string            `json:"content_type"`
	ContentEncoding string            `json:"content_encoding,omitempty"`
	Headers         map[string]string `json:"headers"`
	BodyFile        string            `json:"body_file"`
}

func main() {
	destination := os.Getenv("TIQ_CAPTURE_DIR")
	if destination == "" {
		log.Fatal("TIQ_CAPTURE_DIR is required")
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		log.Fatal(err)
	}

	var sequence atomic.Uint64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
		if err != nil || len(body) > maxBodyBytes {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		id := sequence.Add(1)
		base := fmt.Sprintf("%03d", id)
		bodyName := base + ".body"
		if err := os.WriteFile(filepath.Join(destination, bodyName), body, 0o600); err != nil {
			http.Error(w, "capture failed", http.StatusInternalServerError)
			return
		}
		headers := make(map[string]string)
		for key, values := range r.Header {
			if key == "Authorization" || key == "X-Otlp-Api-Key" {
				continue
			}
			headers[key] = values[0]
		}
		metadata := requestMetadata{
			CapturedAt:      time.Now().UTC(),
			Method:          r.Method,
			Path:            r.URL.Path,
			ContentType:     r.Header.Get("Content-Type"),
			ContentEncoding: r.Header.Get("Content-Encoding"),
			Headers:         headers,
			BodyFile:        bodyName,
		}
		encoded, err := json.MarshalIndent(metadata, "", "  ")
		if err != nil {
			http.Error(w, "capture failed", http.StatusInternalServerError)
			return
		}
		if err := os.WriteFile(filepath.Join(destination, base+".json"), encoded, 0o600); err != nil {
			http.Error(w, "capture failed", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})

	server := &http.Server{
		Addr:              "127.0.0.1:4318",
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	log.Printf("capturing OTLP HTTP on %s into %s", server.Addr, destination)
	log.Fatal(server.ListenAndServe())
}
