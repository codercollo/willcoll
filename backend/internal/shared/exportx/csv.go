// Package exportx holds the product-agnostic CSV import/export primitives
// every product's bulk-import endpoints (units, tenants, customers, owners,
// ...) build on: reading a size-capped upload from a multipart field or a
// raw body, and stripping the UTF-8 byte order mark Excel adds. Parsing a
// specific CSV shape into domain rows stays in each product's own package.
package exportx

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ReadUpload returns the CSV bytes from a multipart "file" field on r, or
// r's raw body, refusing anything over maxBytes.
func ReadUpload(w http.ResponseWriter, r *http.Request, maxBytes int64) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+4096)

	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		file, _, err := r.FormFile("file")
		if err != nil {
			return nil, errors.New(`send the CSV in a multipart "file" field`)
		}
		defer func() { _ = file.Close() }()
		return readLimited(file, maxBytes)
	}
	return readLimited(r.Body, maxBytes)
}

func readLimited(r io.Reader, maxBytes int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, errors.New("the file could not be read or is too large")
	}
	if int64(len(b)) > maxBytes {
		return nil, fmt.Errorf("the file is larger than %d MB", maxBytes/(1<<20))
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, errors.New("the file is empty")
	}
	return b, nil
}

// StripBOM removes a leading UTF-8 byte order mark (Excel adds one when
// saving CSV), so it doesn't corrupt the first header column's name.
func StripBOM(raw []byte) []byte {
	return bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
}
