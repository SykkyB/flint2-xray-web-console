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

func TestAuthCacheHit(t *testing.T) {
	c := newAuthCache()
	if c.hit("admin", "s3cret") {
		t.Fatalf("hit on empty cache")
	}
	c.admit("admin", "s3cret")
	if !c.hit("admin", "s3cret") {
		t.Errorf("cache should report hit after admit")
	}
	if c.hit("admin", "wrong") {
		t.Errorf("cache should not match a different password")
	}
	if c.hit("root", "s3cret") {
		t.Errorf("cache should not match a different user")
	}
}

func TestBasicAuthCachesBcrypt(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("s3cret"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	calls := 0
	handler := BasicAuth("admin", string(hash), nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		calls++
		w.WriteHeader(nethttp.StatusOK)
	}))
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/", nil)
		r.SetBasicAuth("admin", "s3cret")
		handler.ServeHTTP(w, r)
		if w.Code != nethttp.StatusOK {
			t.Fatalf("iter %d: code %d", i, w.Code)
		}
	}
	if calls != 5 {
		t.Errorf("inner handler ran %d times, want 5", calls)
	}
}
