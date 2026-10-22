package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/returnit/returnit/internal/logging"
)

// BodyLog configures request-body logging. It is OFF by default: bodies hold personal data (product
// names, shops, notes, prices, push tokens), so turning it on is a conscious decision (LOG_REQUEST_BODY=on)
// that belongs in the privacy policy. Meant for development and short debugging sessions.
type BodyLog struct {
	Enabled  bool
	MaxBytes int // longest body text written to the log; 0 means 2048
}

func (b BodyLog) max() int {
	if b.MaxBytes <= 0 {
		return 2048
	}
	return b.MaxBytes
}

// wants reports whether this request's body should be captured: JSON bodies only. Uploads (multipart:
// receipt files) and health probes are never captured.
func (b BodyLog) wants(r *http.Request, route string) bool {
	if !b.Enabled || r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0 {
		return false
	}
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return false
	}
	if len(route) >= 8 && route[:8] == "/health/" {
		return false
	}
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mt == "application/json"
}

// capture copies up to maxBodyBytes of the body for logging and puts the same bytes back in front of the
// rest of the stream, so handlers (and their own size limits) behave exactly as if nothing had read it.
// It returns nil when the body is larger than the API accepts anyway (the handler will refuse it).
func capture(c *gin.Context) []byte {
	orig := c.Request.Body
	raw, _ := io.ReadAll(io.LimitReader(orig, maxBodyBytes+1))
	c.Request.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(raw), orig), orig}
	if len(raw) > maxBodyBytes {
		return nil
	}
	return raw
}

// attrs turns a captured body into log attributes: valid JSON is logged as nested JSON with secrets
// replaced, anything else as a (cut) string.
func (b BodyLog) attrs(raw []byte) []any {
	if raw == nil {
		return []any{"body_omitted", "larger than the API accepts"}
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		s, cut := truncate(string(raw), b.max())
		out := []any{"body", s, "body_invalid_json", true}
		if cut {
			out = append(out, "body_truncated", true)
		}
		return out
	}
	enc, err := json.Marshal(redactJSON(v))
	if err != nil {
		return nil
	}
	if len(enc) > b.max() {
		s, _ := truncate(string(enc), b.max())
		return []any{"body", s, "body_truncated", true}
	}
	return []any{"body", json.RawMessage(enc)}
}

func truncate(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	// do not cut a multi-byte character in half
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n], true
}

// redactJSON replaces the value of every sensitive key (tokens, passwords, ...), at any depth.
func redactJSON(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if logging.IsSensitiveKey(k) {
				t[k] = "[REDACTED]"
			} else {
				t[k] = redactJSON(val)
			}
		}
		return t
	case []any:
		for i := range t {
			t[i] = redactJSON(t[i])
		}
		return t
	default:
		return v
	}
}
