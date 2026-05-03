package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Configuration holds application settings
type Configuration struct {
	UserAgent     string
	HTTPTimeout   time.Duration
	MaxRetries    int
	RetryInterval time.Duration
}

// DefaultConfig returns default configuration values
func DefaultConfig() Configuration {
	return Configuration{
		UserAgent:     "komootgpx",
		HTTPTimeout:   10 * time.Second,
		MaxRetries:    3,
		RetryInterval: 2 * time.Second,
	}
}

// Models

// GPX represents the root GPX element
type GPX struct {
	XMLName  xml.Name  `xml:"gpx"`
	XMLNS    string    `xml:"xmlns,attr,omitempty"`
	Version  string    `xml:"version,attr"`
	Creator  string    `xml:"creator,attr"`
	Metadata *Metadata `xml:"metadata,omitempty"`
	Tracks   []Track   `xml:"trk"`
}

// Metadata represents GPX metadata
type Metadata struct {
	Name string `xml:"name,omitempty"`
}

// Track represents a GPX track
type Track struct {
	Name     string    `xml:"name,omitempty"`
	Segments []Segment `xml:"trkseg"`
}

// Segment represents a track segment
type Segment struct {
	Points []Point `xml:"trkpt"`
}

// Point represents a track point with validation methods
type Point struct {
	Lat       float64 `xml:"lat,attr"`
	Lon       float64 `xml:"lon,attr"`
	Elevation float64 `xml:"ele"`
}

// Validate checks if the point coordinates are valid
func (p Point) Validate() error {
	if p.Lat < -90 || p.Lat > 90 {
		return fmt.Errorf("invalid latitude: %f", p.Lat)
	}
	if p.Lon < -180 || p.Lon > 180 {
		return fmt.Errorf("invalid longitude: %f", p.Lon)
	}
	return nil
}

// KomootResponse represents the JSON structure from Komoot
type KomootResponse struct {
	Page struct {
		Embedded struct {
			Tour struct {
				Name     string `json:"name"`
				Embedded struct {
					Coordinates *struct {
						Items []struct {
							Lat float64 `json:"lat"`
							Lng float64 `json:"lng"`
							Alt float64 `json:"alt"`
						} `json:"items"`
					} `json:"coordinates"`
				} `json:"_embedded"`
			} `json:"tour"`
		} `json:"_embedded"`
	} `json:"page"`
}

// GPXConverter handles the conversion process
type GPXConverter struct {
	config Configuration
	client *http.Client
	logger *log.Logger
}

// NewGPXConverter creates a new GPXConverter instance
func NewGPXConverter(config Configuration) *GPXConverter {
	return &GPXConverter{
		config: config,
		client: &http.Client{
			Timeout: config.HTTPTimeout,
		},
		logger: log.New(os.Stderr, "komootgpx: ", log.LstdFlags),
	}
}

// makeHTTPRequest makes an HTTP GET request with retries
func (c *GPXConverter) makeHTTPRequest(ctx context.Context, url string) (string, error) {
	var lastError error
	attempts := c.config.MaxRetries
	if attempts < 1 {
		attempts = 1
	}

	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			c.logger.Printf("Retry attempt %d/%d\n", attempt+1, attempts)
			if err := sleepWithContext(ctx, c.config.RetryInterval); err != nil {
				return "", fmt.Errorf("retry canceled: %w", err)
			}
		}

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			lastError = fmt.Errorf("error creating request: %w", err)
			continue
		}

		req.Header.Set("User-Agent", c.config.UserAgent)

		resp, err := c.client.Do(req)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return "", fmt.Errorf("request canceled: %w", ctxErr)
			}
			lastError = fmt.Errorf("error making request: %w", err)
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if readErr != nil {
			lastError = fmt.Errorf("error reading response body: %w", readErr)
			continue
		}
		if closeErr != nil {
			lastError = fmt.Errorf("error closing response body: %w", closeErr)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastError = fmt.Errorf("unexpected status code: %d", resp.StatusCode)
			if !shouldRetryStatus(resp.StatusCode) {
				return "", lastError
			}
			continue
		}

		return string(body), nil
	}

	return "", fmt.Errorf("all retry attempts failed: %w", lastError)
}

func shouldRetryStatus(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests || statusCode >= 500
}

func sleepWithContext(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}

	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ConvertKomootToGPX performs the complete conversion process
func (c *GPXConverter) ConvertKomootToGPX(ctx context.Context, url, outputPath string) error {
	// Download and process the tour data
	c.logger.Printf("Downloading tour data from %s\n", url)
	html, err := c.makeHTTPRequest(ctx, url)
	if err != nil {
		return fmt.Errorf("failed to download tour data: %w", err)
	}

	c.logger.Println("Extracting JSON data from HTML")
	jsonData, err := extractJSONFromHTML(html)
	if err != nil {
		return fmt.Errorf("failed to extract JSON data: %w", err)
	}

	var komootResp KomootResponse
	if err := json.Unmarshal(jsonData, &komootResp); err != nil {
		return fmt.Errorf("failed to parse JSON data: %w", err)
	}

	gpx, err := c.jsonToGPX(&komootResp)
	if err != nil {
		return fmt.Errorf("failed to convert to GPX: %w", err)
	}

	if err := writeGPX(gpx, outputPath); err != nil {
		return fmt.Errorf("failed to write GPX file: %w", err)
	}

	c.logger.Printf("Successfully created GPX file: %s\n", outputPath)
	return nil
}

// jsonToGPX converts JSON data to GPX format
func (c *GPXConverter) jsonToGPX(data *KomootResponse) (*GPX, error) {
	if data.Page.Embedded.Tour.Embedded.Coordinates == nil {
		return nil, fmt.Errorf("coordinates missing in tour data")
	}

	tourName := data.Page.Embedded.Tour.Name
	coordinates := data.Page.Embedded.Tour.Embedded.Coordinates.Items
	if len(coordinates) == 0 {
		return nil, fmt.Errorf("no coordinates found in tour data")
	}

	gpx := &GPX{
		XMLNS:   "http://www.topografix.com/GPX/1/1",
		Version: "1.1",
		Creator: c.config.UserAgent,
		Tracks: []Track{
			{
				Name: tourName,
				Segments: []Segment{
					{Points: make([]Point, 0, len(coordinates))},
				},
			},
		},
	}
	if tourName != "" {
		gpx.Metadata = &Metadata{Name: tourName}
	}

	for _, item := range coordinates {
		point := Point{
			Lat:       item.Lat,
			Lon:       item.Lng,
			Elevation: item.Alt,
		}

		if err := point.Validate(); err != nil {
			return nil, fmt.Errorf("invalid point data: %w", err)
		}

		gpx.Tracks[0].Segments[0].Points = append(gpx.Tracks[0].Segments[0].Points, point)
	}

	return gpx, nil
}

// extractJSONFromHTML extracts JSON data embedded in the HTML content
func extractJSONFromHTML(htmlContent string) ([]byte, error) {
	startMarker := `kmtBoot.setProps(`

	startIdx := strings.Index(htmlContent, startMarker)
	if startIdx == -1 {
		return nil, fmt.Errorf("start marker not found in HTML content")
	}
	startIdx += len(startMarker)
	for startIdx < len(htmlContent) && (htmlContent[startIdx] == ' ' || htmlContent[startIdx] == '\t' || htmlContent[startIdx] == '\n' || htmlContent[startIdx] == '\r') {
		startIdx++
	}

	literal, err := extractJSONStringLiteral(htmlContent[startIdx:])
	if err != nil {
		return nil, err
	}

	var jsonStr string
	if err := json.Unmarshal([]byte(literal), &jsonStr); err != nil {
		return nil, fmt.Errorf("failed to decode boot JSON string: %w", err)
	}

	return []byte(jsonStr), nil
}

func extractJSONStringLiteral(input string) (string, error) {
	if input == "" || input[0] != '"' {
		return "", fmt.Errorf("kmtBoot.setProps argument is not a JSON string literal")
	}

	escaped := false
	for idx := 1; idx < len(input); idx++ {
		switch {
		case escaped:
			escaped = false
		case input[idx] == '\\':
			escaped = true
		case input[idx] == '"':
			return input[:idx+1], nil
		}
	}

	return "", fmt.Errorf("unterminated kmtBoot.setProps JSON string literal")
}

// writeGPX writes GPX data to a file
func writeGPX(gpx *GPX, filename string) (err error) {
	file, err := os.CreateTemp(filepath.Dir(filename), "."+filepath.Base(filename)+".*.tmp")
	if err != nil {
		return fmt.Errorf("error creating temporary file: %w", err)
	}
	tempName := file.Name()
	defer func() {
		if err != nil {
			_ = file.Close()
			_ = os.Remove(tempName)
		}
	}()

	encoder := xml.NewEncoder(file)
	encoder.Indent("", "  ")

	if _, err = file.WriteString(xml.Header); err != nil {
		return fmt.Errorf("error writing XML header: %w", err)
	}

	if err = encoder.Encode(gpx); err != nil {
		return fmt.Errorf("error encoding GPX: %w", err)
	}

	if err = file.Close(); err != nil {
		return fmt.Errorf("error closing GPX file: %w", err)
	}

	if err = os.Rename(tempName, filename); err != nil {
		return fmt.Errorf("error moving GPX file into place: %w", err)
	}

	return nil
}

// removeQueryParamFromURL removes query parameters from a URL
func removeQueryParamFromURL(urlString string) (string, error) {
	parsedURL, err := url.Parse(urlString)
	if err != nil {
		return "", fmt.Errorf("error parsing URL: %w", err)
	}

	// Remove query parameters
	parsedURL.RawQuery = ""

	return parsedURL.String(), nil
}

func main() {
	var output string
	flag.StringVar(&output, "o", "", "The GPX file to create")
	flag.StringVar(&output, "output", "", "The GPX file to create")
	flag.Parse()

	if flag.NArg() != 1 {
		fmt.Println("Please provide exactly one Komoot URL")
		flag.Usage()
		os.Exit(1)
	}

	if output == "" {
		fmt.Println("Please specify an output file using -o or --output")
		flag.Usage()
		os.Exit(1)
	}

	// Remove query parameters from the URL since they are not needed
	url, err := removeQueryParamFromURL(flag.Arg(0))
	if err != nil {
		log.Fatalf("Error removing query parameters: %v", err)
	}

	config := DefaultConfig()
	converter := NewGPXConverter(config)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := converter.ConvertKomootToGPX(ctx, url, output); err != nil {
		converter.logger.Fatalf("Error converting tour: %v", err)
	}
}
