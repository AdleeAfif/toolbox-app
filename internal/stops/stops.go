package stops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	maxPlaceChars  = 200
	maxRouteMeters = 300_000
	corridorMeters = 1000
	simplifyMeters = 50
	maxQueryPoints = 400
	maxPlaces      = 40
	overheadMeters = 60
	nodeAreaMeters = 250
	maxBody        = 8 << 20
	callTimeout    = 30 * time.Second
	// The public Overpass server can take 40 s or more under load.
	overpassTimeout  = 60 * time.Second
	overpassFastFail = 10 * time.Second
	// Cloudflare drops an origin response after 100 s.
	searchTimeout = 90 * time.Second
	userAgent     = "toolbox-app (https://toolbox.adlee.my)"
)

// MsgUpstream is shown when a map service fails. The cause is only logged.
const MsgUpstream = "the map service is not responding, try again in a minute"

// InputError is a problem with what the user sent, safe to show them.
type InputError struct{ msg string }

func (e *InputError) Error() string { return e.msg }

var (
	ErrNoStart = &InputError{"allow location or type where you are starting from"}
	ErrNoDest  = &InputError{"enter a destination"}
	ErrTooLong = &InputError{"place names are at most 200 characters"}
	ErrNoRoute = &InputError{"no driving route between these places"}
	ErrTooFar  = &InputError{"route is longer than 300 km, pick a closer destination"}
	ErrKind    = &InputError{"pick what you are looking for"}
	ErrBusy    = &InputError{"another search is running, try again in a moment"}
)

func notFound(place string) error {
	return &InputError{`could not find "` + place + `"`}
}

// Labels, from easiest to stop at to hardest.
const (
	LabelEasy      = "Easy stop"
	LabelRR        = "Easy stop · R&R"
	LabelOtherSide = "R&R on the other side"
	LabelExit      = "Near the route, may need an exit"
)

// kind's filters are exact tag matches, unioned. Overpass answers those from its
// tag index, where a regex on the value has to scan.
type kind struct {
	plural  string
	unnamed string
	filters []string
	brand   bool
	match   func(tags map[string]string) bool
}

var areaFilters = []string{`nwr["highway"="services"]`, `nwr["highway"="rest_area"]`}

var kinds = map[string]kind{
	"fuel": {
		plural: "petrol stations", unnamed: "Unnamed petrol station", brand: true,
		filters: []string{`nwr["amenity"="fuel"]`},
		match:   func(t map[string]string) bool { return t["amenity"] == "fuel" },
	},
	"food": {
		plural: "food places", unnamed: "Unnamed restaurant",
		filters: []string{`nwr["amenity"="restaurant"]`, `nwr["amenity"="fast_food"]`, `nwr["amenity"="food_court"]`},
		match: func(t map[string]string) bool {
			a := t["amenity"]
			return a == "restaurant" || a == "fast_food" || a == "food_court"
		},
	},
	"cafe": {
		plural: "cafes", unnamed: "Unnamed cafe",
		filters: []string{`nwr["amenity"="cafe"]`},
		match:   func(t map[string]string) bool { return t["amenity"] == "cafe" },
	},
	"mosque": {
		plural: "mosques or surau", unnamed: "Unnamed mosque or surau",
		filters: []string{`nwr["amenity"="place_of_worship"]["religion"="muslim"]`},
		match: func(t map[string]string) bool {
			return t["amenity"] == "place_of_worship" && t["religion"] == "muslim"
		},
	},
}

// Place is one stop along the route.
type Place struct {
	Name      string
	Label     string
	Area      string
	Easy      bool
	Lat, Lng  float64
	KmAhead   float64
	MetersOff int
}

// GoogleURL opens the place in Google Maps.
func (p Place) GoogleURL() string {
	return "https://www.google.com/maps/search/?api=1&query=" + p.coords()
}

// WazeURL starts Waze navigation to the place.
func (p Place) WazeURL() string {
	return "https://waze.com/ul?ll=" + p.coords() + "&navigate=yes"
}

func (p Place) coords() string {
	return strconv.FormatFloat(p.Lat, 'f', 6, 64) + "," + strconv.FormatFloat(p.Lng, 'f', 6, 64)
}

// Result is the search output. Total counts every place found, Places holds at most 40.
type Result struct {
	Plural  string
	RouteKm float64
	Total   int
	Places  []Place
}

// Finder searches OpenStreetMap services. Run one Finder per process: it allows
// one search at a time and spaces Nominatim calls a second apart.
type Finder struct {
	client       *http.Client
	nominatimURL string
	osrmURL      string
	overpassURL  string

	gate     chan struct{}
	wait     time.Duration
	geoGap   time.Duration
	retryGap time.Duration

	mu      sync.Mutex
	lastGeo time.Time
}

// New returns a Finder using the given service base URLs. overpassURL is the
// full interpreter endpoint.
func New(nominatimURL, osrmURL, overpassURL string) *Finder {
	return &Finder{
		client:       &http.Client{},
		nominatimURL: strings.TrimRight(nominatimURL, "/"),
		osrmURL:      strings.TrimRight(osrmURL, "/"),
		overpassURL:  overpassURL,
		gate:         make(chan struct{}, 1),
		wait:         10 * time.Second,
		geoGap:       time.Second,
		retryGap:     2 * time.Second,
	}
}

// Default returns a Finder on the public OpenStreetMap servers.
func Default() *Finder {
	return New("https://nominatim.openstreetmap.org", "https://router.project-osrm.org", "https://overpass-api.de/api/interpreter")
}

// Search finds places of kindName within 1 km of the driving route from one
// place to another, ordered by distance along the route.
func (f *Finder) Search(ctx context.Context, from, to, kindName string) (Result, error) {
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	switch {
	case from == "":
		return Result{}, ErrNoStart
	case to == "":
		return Result{}, ErrNoDest
	case utf8.RuneCountInString(from) > maxPlaceChars || utf8.RuneCountInString(to) > maxPlaceChars:
		return Result{}, ErrTooLong
	}
	k, ok := kinds[kindName]
	if !ok {
		return Result{}, ErrKind
	}

	timer := time.NewTimer(f.wait)
	defer timer.Stop()
	select {
	case f.gate <- struct{}{}:
		defer func() { <-f.gate }()
	case <-timer.C:
		return Result{}, ErrBusy
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()

	start, err := f.locate(ctx, from)
	if err != nil {
		return Result{}, err
	}
	end, err := f.locate(ctx, to)
	if err != nil {
		return Result{}, err
	}
	r, err := f.route(ctx, start, end)
	if err != nil {
		return Result{}, err
	}
	if r.meters > maxRouteMeters {
		return Result{}, ErrTooFar
	}
	raw, areas, err := f.overpass(ctx, k, simplify(r.pts, simplifyMeters, maxQueryPoints))
	if err != nil {
		return Result{}, err
	}
	places, total := rank(raw, areas, r, maxPlaces)
	return Result{Plural: k.plural, RouteKm: r.meters / 1000, Total: total, Places: places}, nil
}

func (f *Finder) locate(ctx context.Context, place string) (Point, error) {
	if p, ok := parseCoords(place); ok {
		return p, nil
	}
	if err := f.pace(ctx); err != nil {
		return Point{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	q := url.Values{"format": {"jsonv2"}, "limit": {"1"}, "countrycodes": {"my,sg"}, "q": {place}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.nominatimURL+"/search?"+q.Encode(), nil)
	if err != nil {
		return Point{}, upstream("nominatim", err)
	}
	req.Header.Set("Accept-Language", "en")
	var hits []struct {
		Lat string `json:"lat"`
		Lon string `json:"lon"`
	}
	status, err := f.fetch(req, "nominatim", &hits)
	if err != nil {
		return Point{}, err
	}
	if status != http.StatusOK {
		return Point{}, upstream("nominatim", fmt.Errorf("status %d", status))
	}
	if len(hits) == 0 {
		return Point{}, notFound(place)
	}
	lat, err1 := strconv.ParseFloat(hits[0].Lat, 64)
	lng, err2 := strconv.ParseFloat(hits[0].Lon, 64)
	if err1 != nil || err2 != nil {
		return Point{}, upstream("nominatim", errors.New("bad coordinates in response"))
	}
	return Point{Lat: lat, Lng: lng}, nil
}

// pace keeps Nominatim calls at least geoGap apart, as its usage policy asks.
func (f *Finder) pace(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if wait := f.geoGap - time.Since(f.lastGeo); wait > 0 {
		t := time.NewTimer(wait)
		defer t.Stop()
		select {
		case <-t.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.lastGeo = time.Now()
	return nil
}

type osrmResponse struct {
	Code   string `json:"code"`
	Routes []struct {
		Distance float64 `json:"distance"`
		Legs     []struct {
			Steps []struct {
				Name     string `json:"name"`
				Ref      string `json:"ref"`
				Geometry struct {
					Coordinates [][]float64 `json:"coordinates"`
				} `json:"geometry"`
				Intersections []struct {
					Classes []string `json:"classes"`
				} `json:"intersections"`
			} `json:"steps"`
		} `json:"legs"`
	} `json:"routes"`
}

func (f *Finder) route(ctx context.Context, a, b Point) (route, error) {
	u := f.osrmURL + "/route/v1/driving/" + ff(a.Lng) + "," + ff(a.Lat) + ";" + ff(b.Lng) + "," + ff(b.Lat) +
		"?overview=false&geometries=geojson&steps=true"
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return route{}, upstream("osrm", err)
	}
	var body osrmResponse
	status, err := f.fetch(req, "osrm", &body)
	if err != nil {
		return route{}, err
	}
	switch {
	case body.Code == "NoRoute" || body.Code == "NoSegment":
		return route{}, ErrNoRoute
	case status != http.StatusOK || body.Code != "Ok":
		return route{}, upstream("osrm", fmt.Errorf("status %d, code %q", status, body.Code))
	case len(body.Routes) == 0:
		return route{}, ErrNoRoute
	}

	rt := body.Routes[0]
	var pts []Point
	var motorway []bool
	for _, leg := range rt.Legs {
		for _, st := range leg.Steps {
			mw := isExpressway(st.Name, st.Ref)
			for _, in := range st.Intersections {
				if slices.Contains(in.Classes, "motorway") {
					mw = true
				}
			}
			for _, c := range st.Geometry.Coordinates {
				if len(c) < 2 {
					continue
				}
				p := Point{Lat: c[1], Lng: c[0]}
				if n := len(pts); n > 0 && pts[n-1] == p {
					continue
				}
				if len(pts) > 0 {
					motorway = append(motorway, mw)
				}
				pts = append(pts, p)
			}
		}
	}
	if len(pts) < 2 {
		return route{}, ErrNoRoute
	}
	return newRoute(pts, motorway, rt.Distance), nil
}

var expresswayRef = regexp.MustCompile(`^E\d+$`)

// isExpressway reports whether a step is on an expressway. The public OSRM
// server does not send the motorway class, so this reads the road number
// (Malaysian expressways are E1, E2, ...) and the name.
func isExpressway(name, ref string) bool {
	for _, r := range strings.Split(ref, ";") {
		if expresswayRef.MatchString(strings.TrimSpace(r)) {
			return true
		}
	}
	n := strings.ToLower(name)
	return strings.Contains(n, "expressway") || strings.Contains(n, "lebuhraya")
}

type osmElement struct {
	Type   string  `json:"type"`
	ID     int64   `json:"id"`
	Lat    float64 `json:"lat"`
	Lon    float64 `json:"lon"`
	Center *struct {
		Lat float64 `json:"lat"`
		Lon float64 `json:"lon"`
	} `json:"center"`
	Geometry []struct {
		Lat float64 `json:"lat"`
		Lon float64 `json:"lon"`
	} `json:"geometry"`
	Bounds *struct {
		MinLat float64 `json:"minlat"`
		MinLon float64 `json:"minlon"`
		MaxLat float64 `json:"maxlat"`
		MaxLon float64 `json:"maxlon"`
	} `json:"bounds"`
	Tags map[string]string `json:"tags"`
}

func (el osmElement) point() (Point, bool) {
	if el.Center != nil {
		return Point{Lat: el.Center.Lat, Lng: el.Center.Lon}, true
	}
	if el.Type == "node" {
		return Point{Lat: el.Lat, Lng: el.Lon}, true
	}
	return Point{}, false
}

// area is an R&R or rest area. One mapped as a single node has no outline.
type area struct {
	name   string
	poly   []Point
	center Point
	node   bool
}

func (a area) contains(p Point) bool {
	if a.node {
		return haversine(a.center, p) <= nodeAreaMeters
	}
	return inPolygon(p, a.poly)
}

func (el osmElement) area() (area, bool) {
	if h := el.Tags["highway"]; h != "services" && h != "rest_area" {
		return area{}, false
	}
	a := area{name: el.Tags["name"]}
	switch {
	case len(el.Geometry) >= 3:
		for _, g := range el.Geometry {
			a.poly = append(a.poly, Point{Lat: g.Lat, Lng: g.Lon})
		}
	case el.Bounds != nil:
		b := el.Bounds
		a.poly = []Point{{b.MinLat, b.MinLon}, {b.MinLat, b.MaxLon}, {b.MaxLat, b.MaxLon}, {b.MaxLat, b.MinLon}}
	default:
		p, ok := el.point()
		if !ok {
			return area{}, false
		}
		a.center, a.node = p, true
	}
	return a, true
}

type rawPlace struct {
	pt   Point
	name string
}

func overpassQuery(k kind, line []Point) string {
	var b strings.Builder
	b.WriteString("(around:" + strconv.Itoa(corridorMeters))
	for _, p := range line {
		b.WriteString("," + ff(p.Lat) + "," + ff(p.Lng))
	}
	b.WriteString(")")
	around := b.String()
	union := func(filters []string) string {
		var u strings.Builder
		u.WriteString("(")
		for _, f := range filters {
			u.WriteString(f + around + ";")
		}
		u.WriteString(");")
		return u.String()
	}
	return "[out:json][timeout:55];\n" +
		union(k.filters) + "\nout center;\n" +
		union(areaFilters) + "\nout geom;\n"
}

type overpassResponse struct {
	Remark   string       `json:"remark"`
	Elements []osmElement `json:"elements"`
}

func (f *Finder) overpassOnce(ctx context.Context, data string, body *overpassResponse) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.overpassURL, strings.NewReader(data))
	if err != nil {
		return 0, upstream("overpass", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return f.fetch(req, "overpass", body)
}

func (f *Finder) overpass(ctx context.Context, k kind, line []Point) ([]rawPlace, []area, error) {
	ctx, cancel := context.WithTimeout(ctx, overpassTimeout)
	defer cancel()
	data := url.Values{"data": {overpassQuery(k, line)}}.Encode()
	var body overpassResponse
	for try := 0; ; try++ {
		start := time.Now()
		status, err := f.overpassOnce(ctx, data, &body)
		if err == nil && status == http.StatusOK {
			break
		}
		if err == nil {
			err = upstream("overpass", fmt.Errorf("status %d", status))
		}
		// The public server often answers a gateway error within a second or
		// two. Retry that once. A slow failure has used up the time.
		gateway := status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
		if try > 0 || !gateway || time.Since(start) > overpassFastFail {
			return nil, nil, err
		}
		select {
		case <-time.After(f.retryGap):
		case <-ctx.Done():
			return nil, nil, upstream("overpass", ctx.Err())
		}
	}
	if strings.Contains(body.Remark, "error") {
		return nil, nil, upstream("overpass", errors.New(body.Remark))
	}

	var places []rawPlace
	var areas []area
	seen := map[string]bool{}
	for _, el := range body.Elements {
		if a, ok := el.area(); ok {
			areas = append(areas, a)
		}
		id := el.Type + strconv.FormatInt(el.ID, 10)
		if !k.match(el.Tags) || seen[id] {
			continue
		}
		if p, ok := el.point(); ok {
			seen[id] = true
			places = append(places, rawPlace{pt: p, name: placeName(el.Tags, k)})
		}
	}
	return places, areas, nil
}

func placeName(tags map[string]string, k kind) string {
	name := tags["name"]
	if name == "" {
		name = tags["name:en"]
	}
	if b := tags["brand"]; k.brand && b != "" {
		switch {
		case name == "":
			name = b
		case !strings.Contains(strings.ToLower(name), strings.ToLower(b)):
			name = b + " · " + name
		}
	}
	if name == "" {
		name = k.unnamed
	}
	return name
}

// fetch sends req and decodes a JSON body for 200 and 400 responses. OSRM
// reports a missing route as a 400 with a JSON code.
func (f *Finder) fetch(req *http.Request, service string, v any) (int, error) {
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		// url.Error repeats the request URL, which holds the user's places.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return 0, upstream(service, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
		return resp.StatusCode, upstream(service, fmt.Errorf("status %d", resp.StatusCode))
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(v); err != nil {
		return resp.StatusCode, upstream(service, err)
	}
	return resp.StatusCode, nil
}

func upstream(service string, err error) error {
	return fmt.Errorf("%s: %w", service, err)
}

// rank labels each place, sorts by distance along the route, and keeps at most max.
func rank(raw []rawPlace, areas []area, r route, max int) ([]Place, int) {
	out := make([]Place, 0, len(raw))
	for _, rp := range raw {
		seg, along, off, cross := r.nearest(rp.pt)
		label, easy, areaName := classify(rp.pt, r.motorway[seg], off, cross, areas)
		out = append(out, Place{
			Name:      rp.name,
			Label:     label,
			Area:      areaName,
			Easy:      easy,
			Lat:       rp.pt.Lat,
			Lng:       rp.pt.Lng,
			KmAhead:   along / 1000,
			MetersOff: int(math.Round(off/10) * 10),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].KmAhead < out[j].KmAhead })
	total := len(out)
	if len(out) > max {
		out = out[:max]
	}
	return out, total
}

// classify decides how easy a place is to reach. Malaysia drives on the left,
// so on an expressway the R&R for your direction is on your left.
func classify(p Point, motorway bool, off, cross float64, areas []area) (label string, easy bool, areaName string) {
	if !motorway {
		return LabelEasy, true, ""
	}
	for _, a := range areas {
		if !a.contains(p) {
			continue
		}
		if cross > 0 || off <= overheadMeters {
			return LabelRR, true, a.name
		}
		return LabelOtherSide, false, a.name
	}
	return LabelExit, false, ""
}

func ff(v float64) string {
	return strconv.FormatFloat(v, 'f', 6, 64)
}
