package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
)

// maxRequestBytes bounds the approval decision payload. The body only carries
// a comment, so 1 MiB is far above what the database column accepts (4096).
const maxRequestBytes = 1 << 20

var errRequestTooLarge = errors.New("request body exceeds 1 MiB")

// encoder returns a JSON encoder bound to the response writer.
func encoder(w http.ResponseWriter) *json.Encoder {
	return json.NewEncoder(w)
}

// decodeBody reads a JSON request body. An empty body is allowed and decodes
// into the zero value of target: approving without a comment is a valid
// decision, so the body is optional on both decide endpoints.
func decodeBody(r *http.Request, target any) error {
	if r.Body == nil {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxRequestBytes {
		return errRequestTooLarge
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain a single JSON value")
		}
		return err
	}
	return nil
}

// logError records an unexpected handler failure without leaking secrets.
func logError(message string, err error) {
	slog.Error(message, "error", err)
}
