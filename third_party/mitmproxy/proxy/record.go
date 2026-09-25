package proxy

import (
	"net/http"
	"time"
)

// FlowRecord is an immutable, serializable snapshot of an HTTP transaction.
// Unlike Flow (which is a mutable interception-time object tied to live
// connections), FlowRecord is safe for storage, querying, and serialization.
type FlowRecord struct {
	ID              string        `json:"id"`
	Timestamp       time.Time     `json:"timestamp"`
	Method          string        `json:"method"`
	URL             string        `json:"url"`
	Host            string        `json:"host"`
	StatusCode      int           `json:"status_code"`
	ContentType     string        `json:"content_type,omitempty"`
	Duration        time.Duration `json:"duration"`
	RequestHeaders  http.Header   `json:"request_headers,omitempty"`
	ResponseHeaders http.Header   `json:"response_headers,omitempty"`
	RequestBody     []byte        `json:"request_body,omitempty"`
	ResponseBody    []byte        `json:"response_body,omitempty"`
	TLS             bool          `json:"tls"`
	Error           string        `json:"error,omitempty"`
}

// NewFlowRecord converts an interception-time Flow into an immutable record.
// Bodies are truncated to maxBodySnip bytes; pass 0 to keep full bodies.
func NewFlowRecord(f *Flow, maxBodySnip int) *FlowRecord {
	r := &FlowRecord{
		ID:        f.Id.String(),
		Timestamp: f.StartTime,
	}

	if !f.EndTime.IsZero() {
		r.Duration = f.EndTime.Sub(f.StartTime)
	} else if !f.StartTime.IsZero() {
		r.Duration = time.Since(f.StartTime)
	}

	if f.Request != nil {
		r.Method = f.Request.Method
		if f.Request.URL != nil {
			r.URL = f.Request.URL.String()
			r.Host = f.Request.URL.Hostname()
		}
		r.RequestHeaders = f.Request.Header.Clone()
		r.RequestBody = snipBytes(f.Request.Body, maxBodySnip)
	}

	if f.ConnContext != nil && f.ConnContext.ClientConn != nil {
		r.TLS = f.ConnContext.ClientConn.Tls
	}

	if f.Response != nil {
		r.StatusCode = f.Response.StatusCode
		r.ResponseHeaders = f.Response.Header.Clone()
		r.ContentType = f.Response.Header.Get("Content-Type")
		r.ResponseBody = snipBytes(f.Response.Body, maxBodySnip)
	}

	return r
}

func snipBytes(b []byte, max int) []byte {
	if len(b) == 0 {
		return nil
	}
	if max > 0 && len(b) > max {
		b = b[:max]
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
