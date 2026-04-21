package http

import (
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestBasicAuth(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("s3cret"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	handler := BasicAuth("admin", string(hash), nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.WriteHeader(nethttp.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	t.Run("no header", func(t *testing.T) {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
		if w.Code != nethttp.StatusUnauthorized {
			t.Errorf("code: %d", w.Code)
		}
		if got := w.Header().Get("WWW-Authenticate"); got == "" {
			t.Errorf("missing WWW-Authenticate")
		}
	})

	t.Run("bad user", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/", nil)
		r.SetBasicAuth("root", "s3cret")
		handler.ServeHTTP(w, r)
		if w.Code != nethttp.StatusUnauthorized {
			t.Errorf("code: %d", w.Code)
		}
	})

	t.Run("bad password", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/", nil)
		r.SetBasicAuth("admin", "wrong")
		handler.ServeHTTP(w, r)
		if w.Code != nethttp.StatusUnauthorized {
			t.Errorf("code: %d", w.Code)
		}
	})

	t.Run("good credentials", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/", nil)
		r.SetBasicAuth("admin", "s3cret")
		handler.ServeHTTP(w, r)
		if w.Code != nethttp.StatusOK {
			t.Errorf("code: %d, body: %q", w.Code, w.Body.String())
		}
	})
}
