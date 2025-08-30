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
	"encoding/json"
	"encoding/xml"
	"fmt"
	"image"
	"image/draw"
	_ "image/png"
	"image/png"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// --- Structs for Parsing GetCapabilities XML ---
type WMSCapabilities struct {
	Capability struct {
		Layer struct {
			Layer struct {
				Dimension struct {
					Text string `xml:",chardata"`
				} `xml:"Dimension"`
			} `xml:"Layer"`
		} `xml:"Layer"`
	} `xml:"Capability"`
}

// --- WMS and Caching Configuration ---
type WMSInfo struct {
	URL       string
	LayerName string
}

var radarLayers = map[string]WMSInfo{
	"conus":  {"https://opengeo.ncep.noaa.gov/geoserver/conus/conus_bref_qcd/ows", "conus_bref_qcd"},
	"alaska": {"https://opengeo.ncep.noaa.gov/geoserver/alaska/alaska_bref_qcd/ows", "alaska_bref_qcd"},
	"hawaii": {"https://opengeo.ncep.noaa.gov/geoserver/hawaii/hawaii_bref_qcd/ows", "hawaii_bref_qcd"},
	"carib":  {"https://opengeo.ncep.noaa.gov/geoserver/carib/carib_bref_qcd/ows", "carib_bref_qcd"},
	"guam":   {"https://opengeo.ncep.noaa.gov/geoserver/guam/guam_bref_qcd/ows", "guam_bref_qcd"},
}

var hazardsLayer = WMSInfo{"https://opengeo.ncep.noaa.gov/geoserver/wwa/hazards/ows", "hazards"}

const TILE_SIZE = 256
const CACHE_DURATION = 5 * time.Minute

// --- Caching Mechanism ---
type CacheEntry struct {
	Timestamps []string
	Expiry     time.Time
}

var (
	cache      = make(map[string]CacheEntry)
	cacheMutex = &sync.RWMutex{}
)

var client = &http.Client{
	Timeout: 15 * time.Second,
}

// --- Core Logic ---

// getTimestamps fetches and caches the available animation frames for a given area.
func getTimestamps(area string) ([]string, error) {
	cacheMutex.RLock()
	entry, found := cache[area]
	cacheMutex.RUnlock()

	if found && time.Now().Before(entry.Expiry) {
		log.Printf("Returning cached timestamps for '%s'", area)
		return entry.Timestamps, nil
	}

	log.Printf("Fetching new timestamps for '%s'", area)
	wmsInfo, ok := radarLayers[area]
	if !ok {
		return nil, fmt.Errorf("invalid area: %s", area)
	}

	capsURL := fmt.Sprintf("%s?service=wms&version=1.3.0&request=GetCapabilities", wmsInfo.URL)
	resp, err := client.Get(capsURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var caps WMSCapabilities
	if err := xml.Unmarshal(body, &caps); err != nil {
		return nil, err
	}

	timestamps := strings.Split(caps.Capability.Layer.Layer.Dimension.Text, ",")
	frameCount := 12
	if len(timestamps) < frameCount {
		frameCount = len(timestamps)
	}
	recentTimestamps := timestamps[len(timestamps)-frameCount:]

	cacheMutex.Lock()
	cache[area] = CacheEntry{
		Timestamps: recentTimestamps,
		Expiry:     time.Now().Add(CACHE_DURATION),
	}
	cacheMutex.Unlock()

	return recentTimestamps, nil
}

func tileToBoundingBox(x, y, zoom int) (string) {
	resolution := (2 * math.Pi * 6378137) / TILE_SIZE / math.Pow(2, float64(zoom))
	minX := -20037508.3427892 + float64(x)*resolution*TILE_SIZE
	maxY := 20037508.3427892 - float64(y)*resolution*TILE_SIZE
	maxX := minX + resolution*TILE_SIZE
	minY := maxY - resolution*TILE_SIZE
	return fmt.Sprintf("%f,%f,%f,%f", minX, minY, maxX, maxY)
}

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
	params.Add("BBOX", bbox)
	if time != "" {
		params.Add("TIME", time)
	}

	wmsURL := fmt.Sprintf("%s?%s", wms.URL, params.Encode())
	resp, err := client.Get(wmsURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("WMS server returned status %d", resp.StatusCode)
	}

	img, _, err := image.Decode(resp.Body)
	return img, err
}

// --- HTTP Handlers ---

func framesHandler(w http.ResponseWriter, r *http.Request) {
	area := r.URL.Query().Get("area")
	if area == "" {
		area = "conus"
	}

	timestamps, err := getTimestamps(area)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(timestamps)
}

func tileHandler(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	zoom, _ := strconv.Atoi(parts[2])
	x, _ := strconv.Atoi(parts[3])
	y, _ := strconv.Atoi(strings.TrimSuffix(parts[4], ".png"))

	query := r.URL.Query()
	area := query.Get("area")
	if area == "" {
		area = "conus"
	}
	showAlerts, _ := strconv.ParseBool(query.Get("alerts"))
	timestamp := query.Get("time")
	if timestamp == "" {
		timestamps, err := getTimestamps(area)
		if err != nil || len(timestamps) == 0 {
			http.Error(w, "Could not get latest timestamp", http.StatusInternalServerError)
			return
		}
		timestamp = timestamps[len(timestamps)-1]
	}

	radarInfo, _ := radarLayers[area]
	bbox := tileToBoundingBox(x, y, zoom)

	radarImg, err := fetchWmsTile(radarInfo, bbox, timestamp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if showAlerts {
		alertsImg, err := fetchWmsTile(hazardsLayer, bbox, timestamp)
		if err == nil {
			composite := image.NewRGBA(radarImg.Bounds())
			draw.Draw(composite, composite.Bounds(), radarImg, image.Point{}, draw.Src)
			draw.Draw(composite, composite.Bounds(), alertsImg, image.Point{}, draw.Over)
			radarImg = composite
		}
	}

	w.Header().Set("Content-Type", "image/png")
	png.Encode(w, radarImg)
}

func main() {
	http.HandleFunc("/tiles/", tileHandler)
	http.HandleFunc("/frames", framesHandler)
	port := "8080"
	log.Printf("wmsproxy started on %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

