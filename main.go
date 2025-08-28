package main

import (
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// WMS_SERVER_URL is the base URL for the NOAA WMS service.
const WMS_SERVER_URL = "https://opengeo.ncep.noaa.gov/geoserver/conus/conus_bref_qcd/ows"

// TILE_SIZE is the standard dimension for web map tiles.
const TILE_SIZE = 256

// tileToBoundingBox converts XYZ tile coordinates to a geographic bounding box (EPSG:3857).
func tileToBoundingBox(x, y, zoom int) (minX, minY, maxX, maxY float64) {
	// Calculate the resolution (meters per pixel) for the given zoom level
	resolution := (2 * math.Pi * 6378137) / TILE_SIZE / math.Pow(2, float64(zoom))

	// Calculate the coordinates of the top-left corner of the tile in meters
	minX = -20037508.3427892 + float64(x)*resolution*TILE_SIZE
	maxY = 20037508.3427892 - float64(y)*resolution*TILE_SIZE

	// The bottom-right corner is simply the top-left corner plus the tile size in meters
	maxX = minX + resolution*TILE_SIZE
	minY = maxY - resolution*TILE_SIZE

	return minX, minY, maxX, maxY
}

// tileHandler is the core function that handles incoming tile requests.
func tileHandler(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 5 {
		http.Error(w, "Invalid tile URL format. Expected /tiles/{z}/{x}/{y}.png", http.StatusBadRequest)
		return
	}

	zoom, errZ := strconv.Atoi(parts[2])
	x, errX := strconv.Atoi(parts[3])
	yStr := strings.TrimSuffix(parts[4], ".png")
	y, errY := strconv.Atoi(yStr)

	if errZ != nil || errX != nil || errY != nil {
		http.Error(w, "Invalid zoom, x, or y value.", http.StatusBadRequest)
		return
	}

	if zoom > 22 {
	    http.Error(w, "Invalid zoom level.", http.StatusBadRequest)
	    return
	}

	minX, minY, maxX, maxY := tileToBoundingBox(x, y, zoom)
	bbox := fmt.Sprintf("%f,%f,%f,%f", minX, minY, maxX, maxY)

	params := url.Values{}
	params.Add("SERVICE", "WMS")
	params.Add("VERSION", "1.3.0")
	params.Add("REQUEST", "GetMap")
	params.Add("FORMAT", "image/png")
	params.Add("TRANSPARENT", "true")
	params.Add("LAYERS", "conus_bref_qcd") // Layer name from the XML
	params.Add("WIDTH", strconv.Itoa(TILE_SIZE))
	params.Add("HEIGHT", strconv.Itoa(TILE_SIZE))
	params.Add("CRS", "EPSG:3857") // Coordinate Reference System for web maps
	params.Add("STYLES", "")
	params.Add("BBOX", bbox)

	wmsURL := fmt.Sprintf("%s?%s", WMS_SERVER_URL, params.Encode())
	log.Printf("Proxying request for tile z=%d, x=%d, y=%d to WMS URL: %s", zoom, x, y, wmsURL)

	var client = &http.Client{
		Timeout: 10 * time.Second, // 10-second timeout
	}

	resp, err := client.Get(wmsURL)
	if err != nil {
		http.Error(w, "Failed to fetch tile from WMS server", http.StatusInternalServerError)
		log.Printf("Error fetching from WMS: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		http.Error(w, fmt.Sprintf("WMS server returned status %d", resp.StatusCode), resp.StatusCode)
		log.Printf("WMS server error: %s", string(bodyBytes))
		return
	}

	w.Header().Set("Content-Type", "image/png")
	io.Copy(w, resp.Body)
}

func main() {
	http.HandleFunc("/tiles/", tileHandler)

	port := "8080"
	log.Printf("Starting WMS to XYZ proxy server on port %s", port)
	log.Printf("Example tile URL: http://localhost:%s/tiles/10/159/395.png", port)

	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
