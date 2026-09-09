package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/samnart1/space_api/internal/platform/telemetry"
)

type Problem struct {
	Type     string       `json:"type"`
	Title    string       `json:"title"`
	Status   int          `json:"status"`
	Detail   string       `json:"detail,omitempty"`
	Instance string       `json:"instance,omitempty"`
	TraceID  string       `json:"trace_id,omitempty"`
	Errors   []FieldError `json:"errors,omitempty"`
}

type FieldError struct {
	Field  string `json:"field"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

func WriteProblem(w http.ResponseWriter, r *http.Request, p Problem) {
	if p.Type == "" {
		p.Type = "about:blank"
	}
	if p.Title == "" {
		p.Title = http.StatusText(p.Status)
	}
	p.Instance = r.URL.Path
	p.TraceID = RequestIDFrom(r.Context())

	body, err := json.Marshal(p)
	if err != nil {
		telemetry.Logger(r.Context()).Error("encode problem", slog.Any("error", err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_, _ = w.Write(body)
}

func WriteError(w http.ResponseWriter, r *http.Request, status int, detail string) {
	WriteProblem(w, r, Problem{Status: status, Detail: detail})
}

func WriteJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		telemetry.Logger(r.Context()).Error("encode json", slog.Any("error", err))
		WriteError(w, r, http.StatusInternalServerError, "response could not be encode")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
