# WMS to XYZ Tile Proxy

A Go proxy server that converts NOAA WMS radar imagery into standard XYZ map tiles.

## Usage

### Local Development

1.  Ensure you have Go installed.
2.  Run the server from the project root:
    ```bash
    go run .
    ```

### Docker

1.  Build the Docker image:
    ```bash
    docker build -t wms-proxy .
    ```
2.  Run the container:
    ```bash
    docker run -p 8080:8080 wms-proxy
    ```

## Endpoint

The server exposes a single endpoint for fetching tiles.

-   **URL**: `/tiles/{z}/{x}/{y}.png`
-   **Method**: `GET`
-   **Example**: `http://localhost:8080/tiles/8/79/98.png`
