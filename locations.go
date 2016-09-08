package gophermon

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"math/rand"
	"net/http"
	"strings"

	"github.com/kellydunn/golang-geo"

	"github.com/femot/pgoapi-go/api"
)

const (
	NORTH           = 0
	EAST            = 90
	SOUTH           = 180
	WEST            = 270
	ElevationApiURL = "https://maps.googleapis.com/maps/api/elevation/json"
)

// GetAltitude uses Googles elevation API to get the altitude for a given slice of api.Location.
func GetAltitude(locations []*api.Location, key string) ([]float64, error) {
	latLngPairs := make([]string, len(locations))
	elevations := make([]float64, len(locations))
	// Build request URL
	for i, llp := range locations {
		latLngPairs[i] = fmt.Sprintf("%f,%f", llp.Lat, llp.Lon)
	}
	// Docs say 512 per request, but tests were only successful up to 405 requests.
	// See https://developers.google.com/maps/documentation/elevation/usage-limits
	rateLimit := 405
	numRequests := int(math.Ceil(float64(len(locations)) / float64(rateLimit)))
	// Perform request
	for i := 0; i < numRequests; i++ {
		upper := i*rateLimit + rateLimit
		if upper > len(latLngPairs) {
			upper = len(latLngPairs)
		}
		requestURL := fmt.Sprintf("%s?locations=%s&key=%s", ElevationApiURL, strings.Join(latLngPairs[i*rateLimit:upper], "|"), key)
		//log.Fatal(requestURL)
		resp, err := http.Get(requestURL)
		if err != nil {
			return elevations, err
		}
		defer resp.Body.Close()
		// Read response
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return elevations, err
		}
		// Parse response
		response := &ElevationApiResults{}
		err = json.Unmarshal(body, response)
		if err != nil {
			return elevations, err
		}
		for j, e := range response.Results {
			elevations[j+(rateLimit*i)] = e.Elevation
		}
	}
	return elevations, nil
}

// ElevationApiResult is the structure of the individual elevation results sent back by Google's elevation API.
type ElevationApiResult struct {
	Elevation  float64
	Location   api.Location
	Resolution float64
}

// ElevationApiResults is the structure of the response from Google's elevation API.
type ElevationApiResults struct {
	Results []ElevationApiResult
	Status  string
}

// SetCorrectAltitudes uses GetAltitude to set the correct altitude for a slice of api.Location.
func SetCorrectAltitudes(locations []*api.Location, key string) error {
	elevations, err := GetAltitude(locations, key)
	if err != nil {
		return err
	}
	for i, e := range elevations {
		locations[i].Alt = e
	}
	return nil
}

// LocationProvider is a common interface for continuously providing locations.
type LocationProvider interface {
	// NextLocation requests a new location
	NextLocation() *api.Location
	// GetLocations returns all of the providers locations (if applicable).
	GetLocations() []*api.Location
}

// ProvideLocations is a helper function to continuously send locations from a LocationProvider to a channel.
func ProvideLocations(provider LocationProvider, locations chan *api.Location, providers chan LocationProvider) {
	for {
		// Check if we got a new location provider
		select {
		case p := <-providers:
			log.Println("Switching to new LocationProvider")
			provider = p
			// TODO: need a sleep here? or even better a check for distance from old to new location?
		default:
			break
		}
		locations <- provider.NextLocation()
	}
}

// DefaultLocationProvider is a sample implementation of LocationProvider. It contains and returns only one api.Location.
type DefaultLocationProvider struct {
	StartLocation *api.Location
}

func (d DefaultLocationProvider) GetLocations() []*api.Location {
	return []*api.Location{d.StartLocation}
}

func (d DefaultLocationProvider) NextLocation() *api.Location {
	setRandomAccuracy(d.StartLocation)
	return d.StartLocation
}

func setRandomAccuracy(location *api.Location) {
	choices := []float64{5, 5, 5, 10, 10, 30, 50, 65}
	location.Accuracy = choices[rand.Intn(len(choices))]
}

func newLocation(location *api.Location, dist, bearing float64) *api.Location {
	point := geo.NewPoint(location.Lat, location.Lon).PointAtDistanceAndBearing(dist/1000, bearing)
	return &api.Location{
		Lat:      point.Lat(),
		Lon:      point.Lng(),
		Alt:      location.Alt,
		Accuracy: location.Accuracy,
	}
}

// PolygonProvider provides api.Locations that lie within a polygon.
type PolygonProvider struct {
	Polygon         *geo.Polygon
	Locations       []*api.Location
	currentLocation int
}

// NewPolygonProvider creates a new PolygonProvider.
func NewPolygonProvider(polyLocations []api.Location, gmapsKey string) (*PolygonProvider, error) {
	// Create the polygon
	polyPoints := make([]*geo.Point, 0)
	for _, p := range polyLocations {
		polyPoints = append(polyPoints, geo.NewPoint(p.Lat, p.Lon))
	}
	polygon := geo.NewPolygon(polyPoints)
	// Fill polygon with honeycomb
	radius := float64(0)
	start := polyPoints[0]
	for _, p := range polygon.Points() {
		if start.GreatCircleDistance(p) > radius {
			radius = start.GreatCircleDistance(p)
		}
	}
	// Convert to meters
	radius = 1000 * radius
	honey := generateHoneyComb(&api.Location{Lat: start.Lat(), Lon: start.Lng()}, radius, 70)
	final := make([]*api.Location, 0)
	// Filter honeycomb with polygon
	for _, h := range honey {
		if polygon.Contains(geo.NewPoint(h.Lat, h.Lon)) {
			final = append(final, h)
		}
	}
	// Set Altitudes
	err := SetCorrectAltitudes(final, gmapsKey)
	if err != nil {
		return &PolygonProvider{}, err
	}

	return &PolygonProvider{
		Polygon:         polygon,
		Locations:       final,
		currentLocation: -1,
	}, nil
}

func (p PolygonProvider) GetLocations() []*api.Location {
	return p.Locations
}

func (p *PolygonProvider) NextLocation() *api.Location {
	p.currentLocation += 1
	if p.currentLocation >= len(p.Locations) {
		p.currentLocation = 0
	}
	setRandomAccuracy(p.Locations[p.currentLocation])
	return p.Locations[p.currentLocation]
}

// HoneyCombProvider provides api.Locations around a center point in a honeycomb like pattern.
type HoneyCombProvider struct {
	CenterLocation *api.Location
	Locations      []*api.Location
	CurrentStep    int
	Radius         float64 // In meters
}

func generateHoneyComb(center *api.Location, radius, distance float64) []*api.Location {
	currentLocation := center
	locations := make([]*api.Location, 0)
	dx := math.Sqrt(3) * distance // distance between column centers
	dy := 3 * (distance / 2)      // distance between row centers
	rings := int(math.Ceil(radius / dx))

	for ring := 1; ring < rings; ring++ {
		// Move 1 to right
		currentLocation = newLocation(currentLocation, dx, EAST)
		for i := 0; i < ring; i++ {
			// top right
			currentLocation = newLocation(currentLocation, dy, NORTH)
			currentLocation = newLocation(currentLocation, dx/2, WEST)
			locations = append(locations, currentLocation)
		}
		for i := 0; i < ring; i++ {
			// top
			currentLocation = newLocation(currentLocation, dx, WEST)
			locations = append(locations, currentLocation)
		}
		for i := 0; i < ring; i++ {
			// top left
			currentLocation = newLocation(currentLocation, dx/2, WEST)
			currentLocation = newLocation(currentLocation, dy, SOUTH)
			locations = append(locations, currentLocation)
		}
		for i := 0; i < ring; i++ {
			// bottom left
			currentLocation = newLocation(currentLocation, dy, SOUTH)
			currentLocation = newLocation(currentLocation, dx/2, EAST)
			locations = append(locations, currentLocation)
		}
		for i := 0; i < ring; i++ {
			// bottom
			currentLocation = newLocation(currentLocation, dx, EAST)
			locations = append(locations, currentLocation)
		}
		for i := 0; i < ring; i++ {
			// bottom right
			currentLocation = newLocation(currentLocation, dx/2, EAST)
			currentLocation = newLocation(currentLocation, dy, NORTH)
			locations = append(locations, currentLocation)
		}
	}
	return locations
}

// NewHoneyCombProvider creates a new HoneyCombProvider.
func NewHoneyCombProvider(center *api.Location, radius, distance float64) HoneyCombProvider {
	// Init empty HoneyCombProvider
	h := HoneyCombProvider{
		CenterLocation: center,
		Radius:         radius,
		CurrentStep:    -1,
	}
	// Generate locations
	h.Locations = append(h.Locations, generateHoneyComb(center, radius, distance)...)
	return h
}

func (h HoneyCombProvider) GetLocations() []*api.Location {
	return h.Locations
}

func (h *HoneyCombProvider) NextLocation() *api.Location {
	h.CurrentStep += 1
	if h.CurrentStep >= len(h.Locations) {
		h.CurrentStep = 0
	}
	setRandomAccuracy(h.Locations[h.CurrentStep])
	return h.Locations[h.CurrentStep]
}
