/*
   Copyright 2025 blockarchitech

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package main

import (
	"fmt"
	"image"
	"image/draw"
	_ "image/png" // Import for decoding PNGs
	"image/png"   // Import for encoding PNGs
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// WMSInfo holds the details for a specific WMS layer.
type WMSInfo struct {
	URL       string
	LayerName string
}

// radarLayers maps the user-friendly area names to their WMS details.
var radarLayers = map[string]WMSInfo{
	"conus":  {"https://opengeo.ncep.noaa.gov/geoserver/conus/conus_bref_qcd/ows", "conus_bref_qcd"},
	"alaska": {"https://opengeo.ncep.noaa.gov/geoserver/alaska/alaska_bref_qcd/ows", "alaska_bref_qcd"},
	"hawaii": {"https://opengeo.ncep.noaa.gov/geoserver/hawaii/hawaii_bref_qcd/ows", "hawaii_bref_qcd"},
	"carib":  {"https://opengeo.ncep.noaa.gov/geoserver/carib/carib_bref_qcd/ows", "carib_bref_qcd"},
	"guam":   {"https://opengeo.ncep.noaa.gov/geoserver/guam/guam_bref_qcd/ows", "guam_bref_qcd"},
}

// hazardsLayer defines the WMS details for the weather alerts overlay.
var hazardsLayer = WMSInfo{"https://opengeo.ncep.noaa.gov/geoserver/wwa/hazards/ows", "hazards"}

const TILE_SIZE = 256

// client is a shared HTTP client
var client = &http.Client{
	Timeout: 15 * time.Second,
}

// tileToBoundingBox converts XYZ tile coordinates to a geographic bounding box.
func tileToBoundingBox(x, y, zoom int) (minX, minY, maxX, maxY float64) {
	resolution := (2 * math.Pi * 6378137) / TILE_SIZE / math.Pow(2, float64(zoom))
	minX = -20037508.3427892 + float64(x)*resolution*TILE_SIZE
	maxY = 20037508.3427892 - float64(y)*resolution*TILE_SIZE
	maxX = minX + resolution*TILE_SIZE
	minY = maxY - resolution*TILE_SIZE
	return minX, minY, maxX, maxY
}

// fetchWmsTile fetches a tile from a WMS server and decodes it into an image.
func fetchWmsTile(wms WMSInfo, bbox string, time string) (image.Image, error) {
	params := url.Values{}
	params.Add("SERVICE", "WMS")
	params.Add("VERSION", "1.3.0")
	params.Add("REQUEST", "GetMap")
	params.Add("FORMAT", "image/png")
	params.Add("TRANSPARENT", "true")
	params.Add("LAYERS", wms.LayerName)
	params.Add("WIDTH", strconv.Itoa(TILE_SIZE))
	params.Add("HEIGHT", strconv.Itoa(TILE_SIZE))
	params.Add("CRS", "EPSG:3857")
	params.Add("STYLES", "")
	params.Add("BBOX", bbox)
	// Only add the TIME parameter if a value is provided.
	if time != "" {
		params.Add("TIME", time)
	}

	wmsURL := fmt.Sprintf("%s?%s", wms.URL, params.Encode())
	resp, err := client.Get(wmsURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch tile from %s: %w", wms.URL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("WMS server %s returned status %d: %s", wms.URL, resp.StatusCode, string(bodyBytes))
	}

	img, _, err := image.Decode(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to decode image from %s: %w", wms.URL, err)
	}
	return img, nil
}

// tileHandler handles incoming tile requests.
func tileHandler(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 5 {
		http.Error(w, "Invalid tile URL format. Expected /tiles/{z}/{x}/{y}.png", http.StatusBadRequest)
		return
	}

	zoom, _ := strconv.Atoi(parts[2])
	x, _ := strconv.Atoi(parts[3])
	yStr := strings.TrimSuffix(parts[4], ".png")
	y, _ := strconv.Atoi(yStr)

	log.Printf("Proxying request for tile z=%d, x=%d, y=%d", zoom, x, y)

	if zoom > 22 {
		http.Error(w, "Invalid zoom level.", http.StatusBadRequest)
		return
	}

	query := r.URL.Query()
	area := strings.ToLower(query.Get("area"))
	if area == "" {
		area = "conus" // Default to CONUS
	}
	showAlers, _ := strconv.ParseBool(query.Get("alerts"))

	radarInfo, ok := radarLayers[area]
	if !ok {
		http.Error(w, "Invalid area specified. Use one of: conus, alaska, hawaii, carib, guam.", http.StatusBadRequest)
		return
	}

	minX, minY, maxX, maxY := tileToBoundingBox(x, y, zoom)
	bbox := fmt.Sprintf("%f,%f,%f,%f", minX, minY, maxX, maxY)

	currentTime := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	radarImg, err := fetchWmsTile(radarInfo, bbox, currentTime)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		log.Println(err)
		return
	}

	if showAlers {
		alertsImg, err := fetchWmsTile(hazardsLayer, bbox, currentTime)
		if err != nil {
			log.Printf("Could not fetch alerts layer: %v", err)
		} else {
			composite := image.NewRGBA(radarImg.Bounds())
			draw.Draw(composite, composite.Bounds(), radarImg, image.Point{}, draw.Src)
			draw.Draw(composite, composite.Bounds(), alertsImg, image.Point{}, draw.Over)
			radarImg = composite
		}
	}

	w.Header().Set("Content-Type", "image/png")
	if err := png.Encode(w, radarImg); err != nil {
		log.Printf("Failed to encode final tile: %v", err)
	}
}

func main() {
	http.HandleFunc("/tiles/", tileHandler)
	port := "8080"
	log.Printf("starting wmsproxy on %s", port)
	if err := http.ListenAndServe("0.0.0.0:"+port, nil); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
