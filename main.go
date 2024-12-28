package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"os"
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
	XMLName xml.Name `xml:"gpx"`
	Version string   `xml:"version,attr"`
	Creator string   `xml:"creator,attr"`
	Name    string   `xml:"name,omitempty"`
	Tracks  []Track  `xml:"trk"`
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
	Elevation float64 `xml:"ele,omitempty"`
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
					Coordinates struct {
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

// Converter handles the conversion process
type Converter struct {
	config Configuration
	client *http.Client
	logger *log.Logger
}

// NewConverter creates a new Converter instance
func NewConverter(config Configuration) *Converter {
	return &Converter{
		config: config,
		client: &http.Client{
			Timeout: config.HTTPTimeout,
		},
		logger: log.New(os.Stderr, "komootgpx: ", log.LstdFlags),
	}
}

// makeHTTPRequest makes an HTTP GET request with retries
func (c *Converter) makeHTTPRequest(ctx context.Context, url string) (string, error) {
	var lastError error

	for attempt := 0; attempt < c.config.MaxRetries; attempt++ {
		if attempt > 0 {
			c.logger.Printf("Retry attempt %d/%d\n", attempt+1, c.config.MaxRetries)
			time.Sleep(c.config.RetryInterval)
		}

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			lastError = fmt.Errorf("error creating request: %w", err)
			continue
		}

		req.Header.Set("User-Agent", c.config.UserAgent)

		resp, err := c.client.Do(req)
		if err != nil {
			lastError = fmt.Errorf("error making request: %w", err)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			lastError = fmt.Errorf("unexpected status code: %d", resp.StatusCode)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			lastError = fmt.Errorf("error reading response body: %w", err)
			continue
		}

		return string(body), nil
	}

	return "", fmt.Errorf("all retry attempts failed: %w", lastError)
}

// extractJSONFromHTML extracts JSON data embedded in the HTML content
func (c *Converter) extractJSONFromHTML(htmlContent string) ([]byte, error) {
	startMarker := `kmtBoot.setProps("`
	endMarker := `");`

	startIdx := strings.Index(htmlContent, startMarker)
	if startIdx == -1 {
		return nil, fmt.Errorf("start marker not found in HTML content")
	}
	startIdx += len(startMarker)

	endIdx := strings.Index(htmlContent[startIdx:], endMarker)
	if endIdx == -1 {
		return nil, fmt.Errorf("end marker not found in HTML content")
	}

	jsonStr := htmlContent[startIdx : startIdx+endIdx]
	jsonStr = html.UnescapeString(jsonStr)
	jsonStr = strings.ReplaceAll(jsonStr, `\\`, `\`)
	jsonStr = strings.ReplaceAll(jsonStr, `\"`, `"`)

	return []byte(jsonStr), nil
}

// Convert performs the complete conversion process
func (c *Converter) Convert(ctx context.Context, url, outputPath string) error {
	// Download and process the tour data
	c.logger.Printf("Downloading tour data from %s\n", url)
	html, err := c.makeHTTPRequest(ctx, url)
	if err != nil {
		return fmt.Errorf("failed to download tour data: %w", err)
	}

	c.logger.Println("Extracting JSON data from HTML")
	jsonData, err := c.extractJSONFromHTML(html)
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

	if err := c.writeGPX(gpx, outputPath); err != nil {
		return fmt.Errorf("failed to write GPX file: %w", err)
	}

	c.logger.Printf("Successfully created GPX file: %s\n", outputPath)
	return nil
}

// jsonToGPX converts JSON data to GPX format
func (c *Converter) jsonToGPX(data *KomootResponse) (*GPX, error) {
	coordinates := data.Page.Embedded.Tour.Embedded.Coordinates.Items
	if len(coordinates) == 0 {
		return nil, fmt.Errorf("no coordinates found in tour data")
	}

	gpx := &GPX{
		Version: "1.1",
		Creator: c.config.UserAgent,
		Name:    data.Page.Embedded.Tour.Name,
		Tracks: []Track{
			{
				Name: data.Page.Embedded.Tour.Name,
				Segments: []Segment{
					{Points: make([]Point, 0, len(coordinates))},
				},
			},
		},
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

// writeGPX writes GPX data to a file
func (c *Converter) writeGPX(gpx *GPX, filename string) error {
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("error creating file: %w", err)
	}
	defer file.Close()

	encoder := xml.NewEncoder(file)
	encoder.Indent("", "  ")

	if _, err := file.WriteString(xml.Header); err != nil {
		return fmt.Errorf("error writing XML header: %w", err)
	}

	if err := encoder.Encode(gpx); err != nil {
		return fmt.Errorf("error encoding GPX: %w", err)
	}

	return nil
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

	url := flag.Arg(0)
	config := DefaultConfig()
	converter := NewConverter(config)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := converter.Convert(ctx, url, output); err != nil {
		converter.logger.Fatalf("Error converting tour: %v", err)
	}
}
