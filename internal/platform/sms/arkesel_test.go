package sms

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestArkeselRequest(t *testing.T) {
	s := NewArkesel("test-key", "Findaspace", false)
	s.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != arkeselURL || r.Method != "POST" || r.Header.Get("api-key") != "test-key" || r.Header.Get("Content-Type") != "application/json" {
			t.Fatal("wrong vendor request")
		}
		var body arkeselRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Sender != "Findaspace" || body.Sandbox || body.Message != "Test verification" || len(body.Recipients) != 1 || body.Recipients[0] != "+233241234567" {
			t.Fatal("wrong SMS payload")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"success"}`)), Header: make(http.Header)}, nil
	})
	if err := s.Send(context.Background(), "+233241234567", "Test verification"); err != nil {
		t.Fatal(err)
	}
}
func TestArkeselRejectsFailedOrMalformedResponse(t *testing.T) {
	for _, tt := range []struct {
		name string
		code int
		body string
	}{
		{"http failure", 401, `{"status":"success"}`},
		{"vendor failure", 200, `{"status":"error","message":"not accepted"}`},
		{"malformed", 200, `not json`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := NewArkesel("test-key", "Findaspace", false)
			s.http.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tt.code, Body: io.NopCloser(strings.NewReader(tt.body)), Header: make(http.Header)}, nil
			})
			if err := s.Send(context.Background(), "+233241234567", "Test"); err == nil {
				t.Fatal("unsuccessful response accepted")
			}
		})
	}
}
func TestFromConfigSelectsLiveSender(t *testing.T) {
	sender, err := FromConfig("arkesel", "test-key", "Findaspace", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := sender.(*ArkeselSender); !ok {
		t.Fatal("live configuration chose development sender")
	}
}
