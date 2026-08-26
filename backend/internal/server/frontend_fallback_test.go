package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestFrontendFallbackRejectsUnknownAPIPathAsJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	mountFrontend(router)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/not-a-real-endpoint", nil))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.Contains(contentType, "application/json") {
		t.Fatalf("content type = %q", contentType)
	}
	if !strings.Contains(recorder.Body.String(), "API endpoint not found") {
		t.Fatalf("body = %q", recorder.Body.String())
	}
}

func TestFrontendFallbackRejectsUnknownUnversionedAPIPathAsJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	mountFrontend(router)

	for _, path := range []string{"/chat/not-a-real-endpoint", "/responses/unknown", "/messages/unknown"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, nil))

		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want %d", path, recorder.Code, http.StatusNotFound)
		}
		if contentType := recorder.Header().Get("Content-Type"); !strings.Contains(contentType, "application/json") {
			t.Fatalf("%s content type = %q", path, contentType)
		}
	}
}

func TestFrontendPageRoutesServeSPAWithNoStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	mountFrontend(router)

	for _, path := range []string{"/models", "/usage", "/admin/errors"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want %d", path, recorder.Code, http.StatusOK)
		}
		if contentType := recorder.Header().Get("Content-Type"); !strings.Contains(contentType, "text/html") {
			t.Fatalf("%s content type = %q", path, contentType)
		}
		if cacheControl := recorder.Header().Get("Cache-Control"); !strings.Contains(cacheControl, "no-store") {
			t.Fatalf("%s cache control = %q", path, cacheControl)
		}
	}
}

func TestFrontendHashedAssetsAreImmutable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	mountFrontend(router)

	index := httptest.NewRecorder()
	router.ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/", nil))
	start := strings.Index(index.Body.String(), `/assets/`)
	if start < 0 {
		t.Fatal("embedded index does not reference a hashed asset")
	}
	remaining := index.Body.String()[start:]
	end := strings.IndexAny(remaining, `"'`)
	if end <= 0 {
		t.Fatalf("could not parse asset path from %q", remaining)
	}
	assetPath := remaining[:end]

	asset := httptest.NewRecorder()
	router.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, assetPath, nil))
	if asset.Code != http.StatusOK {
		t.Fatalf("asset status = %d", asset.Code)
	}
	if cacheControl := asset.Header().Get("Cache-Control"); !strings.Contains(cacheControl, "immutable") {
		t.Fatalf("asset cache control = %q", cacheControl)
	}
}
