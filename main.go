package main

import (
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

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

// Point represents a track point
type Point struct {
	Lat       float64 `xml:"lat,attr"`
	Lon       float64 `xml:"lon,attr"`
	Elevation float64 `xml:"ele,omitempty"`
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

// makeHTTPRequest makes an HTTP GET request and returns the response body
func makeHTTPRequest(url string) (string, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("error creating request: %v", err)
	}

	req.Header.Set("User-Agent", "komootgpx")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error making request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading response body: %v", err)
	}

	return string(body), nil
}

// extractJSONFromHTML extracts JSON data embedded in the HTML content
func extractJSONFromHTML(htmlContent string) ([]byte, error) {
	startMarker := `kmtBoot.setProps("`
	endMarker := `");`

	startIdx := strings.Index(htmlContent, startMarker)
	if startIdx == -1 {
		return nil, fmt.Errorf("start marker not found")
	}
	startIdx += len(startMarker)

	endIdx := strings.Index(htmlContent[startIdx:], endMarker)
	if endIdx == -1 {
		return nil, fmt.Errorf("end marker not found")
	}

	jsonStr := htmlContent[startIdx : startIdx+endIdx]
	jsonStr = html.UnescapeString(jsonStr)
	jsonStr = strings.ReplaceAll(jsonStr, `\\`, `\`)
	jsonStr = strings.ReplaceAll(jsonStr, `\"`, `"`)

	return []byte(jsonStr), nil
}

// jsonToGPX converts JSON data to GPX format
func jsonToGPX(data *KomootResponse) (*GPX, error) {
	if len(data.Page.Embedded.Tour.Embedded.Coordinates.Items) == 0 {
		return nil, fmt.Errorf("no coordinates found in tour data")
	}

	gpx := &GPX{
		Version: "1.1",
		Creator: "komootgpx",
		Name:    data.Page.Embedded.Tour.Name,
		Tracks: []Track{
			{
				Name: data.Page.Embedded.Tour.Name,
				Segments: []Segment{
					{Points: make([]Point, 0)},
				},
			},
		},
	}

	for _, item := range data.Page.Embedded.Tour.Embedded.Coordinates.Items {
		if item.Lat < -90 || item.Lat > 90 || item.Lng < -180 || item.Lng > 180 {
			return nil, fmt.Errorf("invalid coordinates: lat=%f, lng=%f", item.Lat, item.Lng)
		}

		point := Point{
			Lat:       item.Lat,
			Lon:       item.Lng,
			Elevation: item.Alt,
		}
		gpx.Tracks[0].Segments[0].Points = append(gpx.Tracks[0].Segments[0].Points, point)
	}

	return gpx, nil
}

// writeGPX writes GPX data to a file
func writeGPX(gpx *GPX, filename string) error {
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("error creating file: %v", err)
	}
	defer file.Close()

	encoder := xml.NewEncoder(file)
	encoder.Indent("", "  ")

	// Write XML header
	if _, err := file.WriteString(xml.Header); err != nil {
		return fmt.Errorf("error writing XML header: %v", err)
	}

	if err := encoder.Encode(gpx); err != nil {
		return fmt.Errorf("error encoding GPX: %v", err)
	}

	return nil
}

func main() {
	var (
		url    string
		output string
	)

	flag.StringVar(&output, "o", "", "The GPX file to create")
	flag.StringVar(&output, "output", "", "The GPX file to create")
	flag.Parse()

	if flag.NArg() != 1 {
		fmt.Println("Please provide exactly one Komoot URL")
		flag.Usage()
		os.Exit(1)
	}
	url = flag.Arg(0)

	if output == "" {
		fmt.Println("Please specify an output file using -o or --output")
		flag.Usage()
		os.Exit(1)
	}

	// Download and process the tour data
	html, err := makeHTTPRequest(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error making HTTP request: %v\n", err)
		os.Exit(1)
	}

	jsonData, err := extractJSONFromHTML(html)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error extracting JSON from HTML: %v\n", err)
		os.Exit(1)
	}

	var komootResp KomootResponse
	if err := json.Unmarshal(jsonData, &komootResp); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing JSON: %v\n", err)
		os.Exit(1)
	}

	gpx, err := jsonToGPX(&komootResp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating GPX: %v\n", err)
		os.Exit(1)
	}

	if err := writeGPX(gpx, output); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing GPX file: %v\n", err)
		os.Exit(1)
	}
}
