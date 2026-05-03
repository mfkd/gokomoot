# gokomoot

gokomoot creates a [GPX](https://en.wikipedia.org/wiki/GPS_Exchange_Format) file from a Komoot tour.

Go version of [komootgpx](https://github.com/mfkd/komootgpx).

## Installation

Ensure you have Go installed, then run:

```sh
go install github.com/mfkd/gokomoot
```

## Usage

Create a GPX file from a Komoot tour link:

```sh
gokomoot -o route.gpx https://www.komoot.com/smarttour/33303609
```

## Notes

gokomoot reads route data from Komoot's public tour page payload. It does not
authenticate with Komoot, so private tours are not supported, and conversion may
break if Komoot changes its frontend data format.

## Testing

Run the deterministic unit and captured-fixture tests:

```sh
go test ./...
```

The committed fixture in `testdata/` is a reduced capture from the public tour
shown above. It keeps only the real tour name and coordinate payload used by the
converter.

Run an optional live check against a public Komoot URL:

```sh
GOKOMOOT_INTEGRATION_URL=https://www.komoot.com/smarttour/33303609 go test -run TestLiveKomootConversion ./...
```

Refresh the captured fixture from a public Komoot URL:

```sh
GOKOMOOT_CAPTURE_URL=https://www.komoot.com/smarttour/33303609 \
GOKOMOOT_CAPTURE_OUT=testdata/komoot_public_smarttour_33303609.json \
go test -run TestCaptureKomootFixture ./...
```
