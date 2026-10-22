package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/returnit/returnit/internal/logging"
)

// BodyLog configures request- and response-body logging. It is OFF by default: bodies hold personal data (product
// names, shops, notes, prices, push tokens, receipt links), so turning it on is a conscious decision (LOG_REQUEST_BODY=on)
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
	if strings.HasPrefix(route, "/health/") {
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
func (b BodyLog) attrs(prefix string, raw []byte) []any {
	if raw == nil {
		return []any{prefix + "_omitted", "too large to log"}
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		s, cut := truncate(string(raw), b.max())
		out := []any{prefix, s, prefix + "_invalid_json", true}
		if cut {
			out = append(out, prefix+"_truncated", true)
		}
		return out
	}
	enc, err := json.Marshal(redactJSON(v))
	if err != nil {
		return nil
	}
	if len(enc) > b.max() {
		s, _ := truncate(string(enc), b.max())
		return []any{prefix, s, prefix + "_truncated", true}
	}
	return []any{prefix, json.RawMessage(enc)}
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

// responseCap is the most response bytes kept in memory for logging; longer responses are not logged.
const responseCap = 256 << 10

// captureWriter remembers what the handler writes (up to responseCap) while passing everything through.
type captureWriter struct {
	gin.ResponseWriter
	buf  bytes.Buffer
	over bool
}

func (w *captureWriter) keep(b []byte) {
	if w.over {
		return
	}
	if w.buf.Len()+len(b) > responseCap {
		w.over = true
		w.buf.Reset()
		return
	}
	w.buf.Write(b)
}

func (w *captureWriter) Write(b []byte) (int, error) {
	w.keep(b)
	return w.ResponseWriter.Write(b)
}

func (w *captureWriter) WriteString(s string) (int, error) {
	w.keep([]byte(s))
	return w.ResponseWriter.WriteString(s)
}

// wantsResponse is false for health probes and for the full data export (a dump of everything we hold
// about a person is never copied into a log).
func (b BodyLog) wantsResponse(route string) bool {
	if !b.Enabled {
		return false
	}
	return !strings.HasPrefix(route, "/health/") && route != "/api/v1/account/export"
}

// responseAttrs returns the response body as log attributes, or nothing when it was not JSON or empty.
func (b BodyLog) responseAttrs(w *captureWriter) []any {
	if mt, _, err := mime.ParseMediaType(w.Header().Get("Content-Type")); err != nil || mt != "application/json" {
		return nil
	}
	if w.over {
		return b.attrs("response_body", nil)
	}
	if w.buf.Len() == 0 {
		return nil
	}
	return b.attrs("response_body", w.buf.Bytes())
}
