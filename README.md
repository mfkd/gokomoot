# gokomoot

gokomoot creates a [GPX](https://en.wikipedia.org/wiki/GPS_Exchange_Format) file from a Komoot tour.

Go version of [komootgpx](https://github.com/mfkd/komootgpx).

## Installation

```sh
go install github.com/mfkd/gokomoot@latest
```

## Usage

Create a GPX file from a Komoot tour link:

```sh
gokomoot -o route.gpx https://www.komoot.com/smarttour/33303609
```
