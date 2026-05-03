package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const capturedKomootFixture = "testdata/komoot_public_smarttour_33303609.json"

func TestCapturedKomootFixtureConvertsToGPX(t *testing.T) {
	content, err := os.ReadFile(capturedKomootFixture)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v", capturedKomootFixture, err)
	}

	var response KomootResponse
	if err := json.Unmarshal(content, &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	gpx, err := NewGPXConverter(DefaultConfig()).jsonToGPX(&response)
	if err != nil {
		t.Fatalf("jsonToGPX() error = %v", err)
	}

	if gpx.Metadata == nil || !strings.Contains(gpx.Metadata.Name, "Olympia-Stadion") {
		t.Fatalf("metadata name = %#v, want captured public tour name", gpx.Metadata)
	}

	points := gpx.Tracks[0].Segments[0].Points
	if len(points) != 2044 {
		t.Fatalf("captured fixture point count = %d, want 2044", len(points))
	}
	if points[0] != (Point{Lat: 52.516839, Lon: 13.25041, Elevation: 50.4}) {
		t.Fatalf("first point = %#v, want captured fixture start point", points[0])
	}
	if points[len(points)-1] != (Point{Lat: 52.516839, Lon: 13.25041, Elevation: 50.4}) {
		t.Fatalf("last point = %#v, want captured fixture end point", points[len(points)-1])
	}
}

func TestLiveKomootConversion(t *testing.T) {
	liveURL := os.Getenv("GOKOMOOT_INTEGRATION_URL")
	if liveURL == "" {
		t.Skip("set GOKOMOOT_INTEGRATION_URL to run live Komoot conversion test")
	}

	converter := NewGPXConverter(DefaultConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	html, err := converter.makeHTTPRequest(ctx, liveURL)
	if err != nil {
		t.Fatalf("makeHTTPRequest() error = %v", err)
	}

	jsonData, err := extractJSONFromHTML(html)
	if err != nil {
		t.Fatalf("extractJSONFromHTML() error = %v", err)
	}

	var response KomootResponse
	if err := json.Unmarshal(jsonData, &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	gpx, err := converter.jsonToGPX(&response)
	if err != nil {
		t.Fatalf("jsonToGPX() error = %v", err)
	}

	points := gpx.Tracks[0].Segments[0].Points
	if len(points) == 0 {
		t.Fatal("live conversion produced no GPX points")
	}
}

func TestCaptureKomootFixture(t *testing.T) {
	captureURL := os.Getenv("GOKOMOOT_CAPTURE_URL")
	captureOut := os.Getenv("GOKOMOOT_CAPTURE_OUT")
	if captureURL == "" || captureOut == "" {
		t.Skip("set GOKOMOOT_CAPTURE_URL and GOKOMOOT_CAPTURE_OUT to refresh a captured fixture")
	}

	converter := NewGPXConverter(DefaultConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	html, err := converter.makeHTTPRequest(ctx, captureURL)
	if err != nil {
		t.Fatalf("makeHTTPRequest() error = %v", err)
	}

	jsonData, err := extractJSONFromHTML(html)
	if err != nil {
		t.Fatalf("extractJSONFromHTML() error = %v", err)
	}

	var response KomootResponse
	if err := json.Unmarshal(jsonData, &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if _, err := converter.jsonToGPX(&response); err != nil {
		t.Fatalf("jsonToGPX() error = %v", err)
	}

	content, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent() error = %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(captureOut), 0o755); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(captureOut, append(content, '\n'), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
}

func TestExtractJSONFromHTML(t *testing.T) {
	payload := `{"page":{"_embedded":{"tour":{"name":"Tour with \"); marker and &quot; text","_embedded":{"coordinates":{"items":[{"lat":51.5,"lng":-0.12,"alt":35}]}}}}}}`
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	got, err := extractJSONFromHTML(`<script>kmtBoot.setProps(` + string(encodedPayload) + `);</script>`)
	if err != nil {
		t.Fatalf("extractJSONFromHTML() error = %v", err)
	}

	if string(got) != payload {
		t.Fatalf("extractJSONFromHTML() = %q, want %q", got, payload)
	}
}

func TestExtractJSONFromHTMLMissingMarker(t *testing.T) {
	_, err := extractJSONFromHTML(`<script>window.boot = "{}";</script>`)
	if err == nil || !strings.Contains(err.Error(), "start marker not found") {
		t.Fatalf("extractJSONFromHTML() error = %v, want start marker error", err)
	}
}

func TestExtractJSONFromHTMLMalformedLiteral(t *testing.T) {
	_, err := extractJSONFromHTML(`<script>kmtBoot.setProps("unterminated);</script>`)
	if err == nil || !strings.Contains(err.Error(), "unterminated") {
		t.Fatalf("extractJSONFromHTML() error = %v, want unterminated literal error", err)
	}
}

func TestJSONToGPXRequiresCoordinates(t *testing.T) {
	var response KomootResponse
	if err := json.Unmarshal([]byte(`{"page":{"_embedded":{"tour":{"name":"No coords","_embedded":{}}}}}`), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	_, err := NewGPXConverter(DefaultConfig()).jsonToGPX(&response)
	if err == nil || !strings.Contains(err.Error(), "coordinates missing") {
		t.Fatalf("jsonToGPX() error = %v, want coordinates missing error", err)
	}
}

func TestJSONToGPXRejectsEmptyCoordinates(t *testing.T) {
	var response KomootResponse
	if err := json.Unmarshal([]byte(`{"page":{"_embedded":{"tour":{"name":"Empty coords","_embedded":{"coordinates":{"items":[]}}}}}}`), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	_, err := NewGPXConverter(DefaultConfig()).jsonToGPX(&response)
	if err == nil || !strings.Contains(err.Error(), "no coordinates found") {
		t.Fatalf("jsonToGPX() error = %v, want no coordinates found error", err)
	}
}

func TestJSONToGPXRejectsInvalidCoordinates(t *testing.T) {
	var response KomootResponse
	if err := json.Unmarshal([]byte(`{"page":{"_embedded":{"tour":{"name":"Bad coords","_embedded":{"coordinates":{"items":[{"lat":91,"lng":0,"alt":10}]}}}}}}`), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	_, err := NewGPXConverter(DefaultConfig()).jsonToGPX(&response)
	if err == nil || !strings.Contains(err.Error(), "invalid latitude") {
		t.Fatalf("jsonToGPX() error = %v, want invalid latitude error", err)
	}
}

func TestWriteGPXProducesValidGPX11Shape(t *testing.T) {
	gpx := &GPX{
		XMLNS:   "http://www.topografix.com/GPX/1/1",
		Version: "1.1",
		Creator: "gokomoot-test",
		Metadata: &Metadata{
			Name: "Test Tour",
		},
		Tracks: []Track{
			{
				Name: "Test Tour",
				Segments: []Segment{
					{
						Points: []Point{
							{Lat: 51.5, Lon: -0.12, Elevation: 0},
						},
					},
				},
			},
		},
	}

	outputPath := filepath.Join(t.TempDir(), "route.gpx")
	if err := writeGPX(gpx, outputPath); err != nil {
		t.Fatalf("writeGPX() error = %v", err)
	}

	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}

	xmlText := string(content)
	for _, want := range []string{
		`<gpx xmlns="http://www.topografix.com/GPX/1/1" version="1.1" creator="gokomoot-test">`,
		`<metadata>`,
		`<name>Test Tour</name>`,
		`<trk>`,
		`<ele>0</ele>`,
	} {
		if !strings.Contains(xmlText, want) {
			t.Fatalf("GPX output missing %q:\n%s", want, xmlText)
		}
	}

	if strings.Contains(xmlText, "<gpx><name>") {
		t.Fatalf("GPX output contains root-level name:\n%s", xmlText)
	}

	var parsed any
	if err := xml.Unmarshal(content, &parsed); err != nil {
		t.Fatalf("xml.Unmarshal() error = %v", err)
	}
}

func TestMakeHTTPRequestRetriesTransientStatus(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, "try again", http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, "ok")
	}))
	defer server.Close()

	config := DefaultConfig()
	config.MaxRetries = 2
	config.RetryInterval = 0
	converter := NewGPXConverter(config)

	body, err := converter.makeHTTPRequest(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("makeHTTPRequest() error = %v", err)
	}
	if body != "ok" {
		t.Fatalf("makeHTTPRequest() body = %q, want ok", body)
	}
	if calls != 2 {
		t.Fatalf("server calls = %d, want 2", calls)
	}
}

func TestMakeHTTPRequestDoesNotRetryPermanentClientError(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	config := DefaultConfig()
	config.MaxRetries = 3
	config.RetryInterval = 0
	converter := NewGPXConverter(config)

	_, err := converter.makeHTTPRequest(context.Background(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "unexpected status code: 404") {
		t.Fatalf("makeHTTPRequest() error = %v, want 404 error", err)
	}
	if calls != 1 {
		t.Fatalf("server calls = %d, want 1", calls)
	}
}

func TestSleepWithContextCanBeCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	err := sleepWithContext(ctx, time.Hour)
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("sleepWithContext() error = %v, want context canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("sleepWithContext() took %s, want prompt cancellation", elapsed)
	}
}
