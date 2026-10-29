package stops

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestParseCoords(t *testing.T) {
	for in, want := range map[string]bool{
		"3.139, 101.687": true,
		"3.139,101.687":  true,
		"-6.2, 106.8":    true,
		"Petaling Jaya":  false,
		"95, 10":         false,
		"3.1, 190":       false,
		"3.1 101.6":      false,
	} {
		if _, got := parseCoords(in); got != want {
			t.Errorf("parseCoords(%q) = %v, want %v", in, got, want)
		}
	}
	if p, _ := parseCoords("3.139, 101.687"); p.Lat != 3.139 || p.Lng != 101.687 {
		t.Fatalf("got %+v", p)
	}
}

func TestSimplify(t *testing.T) {
	var line []Point
	for i := 0; i < 5000; i++ {
		line = append(line, Point{Lat: 3 + float64(i)*0.0005, Lng: 101.5 + 0.01*math.Sin(float64(i)/3)})
	}
	got := simplify(line, simplifyMeters, maxQueryPoints)
	if len(got) > maxQueryPoints || got[0] != line[0] || got[len(got)-1] != line[len(line)-1] {
		t.Fatalf("len %d, ends %v %v", len(got), got[0], got[len(got)-1])
	}
	straight := simplify([]Point{{3, 101}, {3.5, 101}, {4, 101}}, simplifyMeters, maxQueryPoints)
	if len(straight) != 2 {
		t.Fatalf("straight line kept %d points", len(straight))
	}
}

func northbound(motorway ...bool) route {
	pts := []Point{{3.0, 101.5}}
	step := 0.1 / float64(len(motorway))
	for i := range motorway {
		pts = append(pts, Point{3.0 + step*float64(i+1), 101.5})
	}
	return newRoute(pts, motorway, haversine(pts[0], pts[len(pts)-1]))
}

func near(got, want, tol float64) bool { return math.Abs(got-want) <= tol }

func TestNearestAndSide(t *testing.T) {
	r := northbound(false)
	_, along, off, cross := r.nearest(Point{3.05, 101.505})
	if !near(along, 5560, 20) || !near(off, 555, 5) {
		t.Fatalf("along %.0f off %.0f", along, off)
	}
	if cross >= 0 {
		t.Fatal("a point east of a northbound road should be on the right")
	}
	if _, _, _, cross := r.nearest(Point{3.05, 101.495}); cross <= 0 {
		t.Fatal("a point west of a northbound road should be on the left")
	}
}

func TestInPolygon(t *testing.T) {
	sq := []Point{{3, 101}, {3, 101.01}, {3.01, 101.01}, {3.01, 101}}
	if !inPolygon(Point{3.005, 101.005}, sq) {
		t.Fatal("center should be inside")
	}
	if inPolygon(Point{3.02, 101.005}, sq) {
		t.Fatal("point north of the square should be outside")
	}
}

func TestIsExpressway(t *testing.T) {
	cases := []struct {
		name, ref string
		want      bool
	}{
		{"Lebuhraya Utara–Selatan", "E1", true},
		{"", "E1;AH2", true},
		{"Pan Island Expressway", "PIE", true},
		{"Laluan Persekutuan 1", "1", false},
		{"Jalan Segambut", "", false},
		{"Jalan Ekspres", "E", false},
	}
	for _, c := range cases {
		if got := isExpressway(c.name, c.ref); got != c.want {
			t.Errorf("isExpressway(%q, %q) = %v, want %v", c.name, c.ref, got, c.want)
		}
	}
}

func TestClassify(t *testing.T) {
	left := area{name: "R&R Left", poly: []Point{{3.07, 101.49}, {3.07, 101.499}, {3.08, 101.499}, {3.08, 101.49}}}
	right := area{name: "R&R Right", poly: []Point{{3.07, 101.501}, {3.07, 101.51}, {3.08, 101.51}, {3.08, 101.501}}}
	node := area{name: "Hentian", center: Point{3.09, 101.5}, node: true}
	areas := []area{left, right, node}

	for name, tc := range map[string]struct {
		p        Point
		motorway bool
		off      float64
		cross    float64
		label    string
		easy     bool
		area     string
	}{
		"ordinary road":   {Point{3.075, 101.505}, false, 500, -1, LabelEasy, true, ""},
		"R&R on the left": {Point{3.075, 101.495}, true, 500, 1, LabelRR, true, "R&R Left"},
		"overhead R&R":    {Point{3.075, 101.505}, true, 40, -1, LabelRR, true, "R&R Right"},
		"other side":      {Point{3.075, 101.505}, true, 500, -1, LabelOtherSide, false, "R&R Right"},
		"node area":       {Point{3.0905, 101.5}, true, 20, 1, LabelRR, true, "Hentian"},
		"needs an exit":   {Point{3.2, 101.505}, true, 500, -1, LabelExit, false, ""},
	} {
		label, easy, a := classify(tc.p, tc.motorway, tc.off, tc.cross, areas)
		if label != tc.label || easy != tc.easy || a != tc.area {
			t.Errorf("%s: got %q %v %q", name, label, easy, a)
		}
	}
}

func TestRankSortsAndCaps(t *testing.T) {
	r := northbound(false)
	var raw []rawPlace
	for i := 49; i >= 0; i-- {
		raw = append(raw, rawPlace{pt: Point{3.001 + float64(i)*0.0019, 101.5005}, name: fmt.Sprint(i)})
	}
	got, total := rank(raw, nil, r, maxPlaces)
	if total != 50 || len(got) != maxPlaces {
		t.Fatalf("total %d len %d", total, len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].KmAhead < got[i-1].KmAhead {
			t.Fatalf("not sorted at %d", i)
		}
	}
	if got[0].Name != "0" {
		t.Fatalf("first is %q", got[0].Name)
	}
}

// fake answers as Nominatim, OSRM, and Overpass.
type fake struct {
	srv                *httptest.Server
	geoBody            string
	osrmStatus         int
	osrmBody           string
	overStatus         int
	overBody           string
	geoN, osrmN, overN atomic.Int32
	badAgent           atomic.Bool
	lastQuery          atomic.Value
}

const route11km = `{"code":"Ok","routes":[{"distance":%d,"legs":[{"steps":[
 {"geometry":{"coordinates":[[101.5,3.0],[101.5,3.05]]},"intersections":[{"classes":[]}]},
 {"geometry":{"coordinates":[[101.5,3.05],[101.5,3.1]]},"intersections":[{"classes":["motorway","toll"]}]}
]}]}]}`

// Northbound: a town stop, then on the expressway an R&R on each side and a place off an exit.
const placesBody = `{"elements":[
 {"type":"node","id":1,"lat":3.02,"lon":101.501,"tags":{"amenity":"fuel","name":"Shell Kampung","brand":"Shell"}},
 {"type":"node","id":2,"lat":3.08,"lon":101.497,"tags":{"amenity":"fuel","brand":"Petronas"}},
 {"type":"node","id":3,"lat":3.07,"lon":101.503,"tags":{"amenity":"fuel"}},
 {"type":"node","id":4,"lat":3.09,"lon":101.505,"tags":{"amenity":"fuel","name":"Caltex Bukit"}},
 {"type":"way","id":10,"tags":{"highway":"services","name":"R&R Left"},"geometry":[
  {"lat":3.079,"lon":101.496},{"lat":3.079,"lon":101.498},{"lat":3.081,"lon":101.498},{"lat":3.081,"lon":101.496},{"lat":3.079,"lon":101.496}]},
 {"type":"way","id":11,"tags":{"highway":"services","name":"R&R Right"},"geometry":[
  {"lat":3.069,"lon":101.502},{"lat":3.069,"lon":101.504},{"lat":3.071,"lon":101.504},{"lat":3.071,"lon":101.502},{"lat":3.069,"lon":101.502}]}
]}`

func newFake(t *testing.T) *fake {
	t.Helper()
	fk := &fake{
		geoBody:    `[{"lat":"3.0","lon":"101.5"}]`,
		osrmStatus: http.StatusOK,
		osrmBody:   fmt.Sprintf(route11km, 11120),
		overStatus: http.StatusOK,
		overBody:   placesBody,
	}
	agent := func(r *http.Request) {
		if r.Header.Get("User-Agent") != userAgent {
			fk.badAgent.Store(true)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		agent(r)
		fk.geoN.Add(1)
		fmt.Fprint(w, fk.geoBody)
	})
	mux.HandleFunc("/route/v1/driving/", func(w http.ResponseWriter, r *http.Request) {
		agent(r)
		fk.osrmN.Add(1)
		w.WriteHeader(fk.osrmStatus)
		fmt.Fprint(w, fk.osrmBody)
	})
	mux.HandleFunc("/api/interpreter", func(w http.ResponseWriter, r *http.Request) {
		agent(r)
		fk.overN.Add(1)
		fk.lastQuery.Store(r.PostFormValue("data"))
		w.WriteHeader(fk.overStatus)
		fmt.Fprint(w, fk.overBody)
	})
	fk.srv = httptest.NewServer(mux)
	t.Cleanup(fk.srv.Close)
	return fk
}

func (fk *fake) finder() *Finder {
	f := New(fk.srv.URL, fk.srv.URL, fk.srv.URL+"/api/interpreter")
	f.geoGap = 0
	f.retryGap = 0
	return f
}

func TestSearch(t *testing.T) {
	fk := newFake(t)
	res, err := fk.finder().Search(context.Background(), "Kuala Lumpur", "Rawang", "fuel")
	if err != nil {
		t.Fatal(err)
	}
	if fk.geoN.Load() != 2 || fk.badAgent.Load() {
		t.Fatalf("nominatim calls %d, bad agent %v", fk.geoN.Load(), fk.badAgent.Load())
	}
	if q, _ := fk.lastQuery.Load().(string); !strings.Contains(q, `nwr["amenity"="fuel"](around:1000,`) || !strings.Contains(q, `nwr["highway"="rest_area"](around:1000,`) {
		t.Fatalf("query %q", q)
	}
	if res.Total != 4 || len(res.Places) != 4 || !near(res.RouteKm, 11.12, .01) || res.Plural != "petrol stations" {
		t.Fatalf("result %+v", res)
	}
	want := []struct{ name, label string }{
		{"Shell Kampung", LabelEasy},
		{"Unnamed petrol station", LabelOtherSide},
		{"Petronas", LabelRR},
		{"Caltex Bukit", LabelExit},
	}
	for i, w := range want {
		if p := res.Places[i]; p.Name != w.name || p.Label != w.label {
			t.Errorf("place %d = %q %q, want %q %q", i, p.Name, p.Label, w.name, w.label)
		}
	}
	if a := res.Places[2].Area; a != "R&R Left" {
		t.Errorf("area %q", a)
	}
	if u := res.Places[0].WazeURL(); u != "https://waze.com/ul?ll=3.020000,101.501000&navigate=yes" {
		t.Errorf("waze %q", u)
	}
}

func TestSearchCoordsSkipNominatim(t *testing.T) {
	fk := newFake(t)
	if _, err := fk.finder().Search(context.Background(), "3.0, 101.5", "Rawang", "cafe"); err != nil {
		t.Fatal(err)
	}
	if n := fk.geoN.Load(); n != 1 {
		t.Fatalf("nominatim calls %d, want 1", n)
	}
}

func TestSearchErrors(t *testing.T) {
	ctx := context.Background()
	long := strings.Repeat("a", maxPlaceChars+1)
	for name, tc := range map[string]struct {
		from, to, kind string
		want           error
	}{
		"no start": {"", "Ipoh", "fuel", ErrNoStart},
		"no dest":  {"KL", " ", "fuel", ErrNoDest},
		"too long": {long, "Ipoh", "fuel", ErrTooLong},
		"bad kind": {"KL", "Ipoh", "bar", ErrKind},
	} {
		fk := newFake(t)
		if _, err := fk.finder().Search(ctx, tc.from, tc.to, tc.kind); err != tc.want {
			t.Errorf("%s: %v", name, err)
		}
		if fk.geoN.Load()+fk.osrmN.Load()+fk.overN.Load() != 0 {
			t.Errorf("%s: called a service", name)
		}
	}

	fk := newFake(t)
	fk.geoBody = `[]`
	_, err := fk.finder().Search(ctx, "3.0, 101.5", "Nowhere", "fuel")
	var inputErr *InputError
	if !errors.As(err, &inputErr) || err.Error() != `could not find "Nowhere"` {
		t.Fatalf("not found: %v", err)
	}

	fk = newFake(t)
	fk.osrmStatus, fk.osrmBody = http.StatusBadRequest, `{"code":"NoRoute","routes":[]}`
	if _, err := fk.finder().Search(ctx, "3.0, 101.5", "3.1, 101.5", "fuel"); err != ErrNoRoute {
		t.Fatalf("no route: %v", err)
	}

	fk = newFake(t)
	fk.osrmBody = fmt.Sprintf(route11km, 350000)
	if _, err := fk.finder().Search(ctx, "3.0, 101.5", "3.1, 101.5", "fuel"); err != ErrTooFar || fk.overN.Load() != 0 {
		t.Fatalf("too far: %v, overpass calls %d", err, fk.overN.Load())
	}

	fk = newFake(t)
	fk.overStatus, fk.overBody = http.StatusGatewayTimeout, "busy"
	_, err = fk.finder().Search(ctx, "3.0, 101.5", "3.1, 101.5", "fuel")
	if err == nil || errors.As(err, &inputErr) || !strings.Contains(err.Error(), "overpass") || fk.overN.Load() != 2 {
		t.Fatalf("overpass down: %v, calls %d", err, fk.overN.Load())
	}

	fk = newFake(t)
	fk.overStatus, fk.overBody = http.StatusTooManyRequests, "slow down"
	if _, err := fk.finder().Search(ctx, "3.0, 101.5", "3.1, 101.5", "fuel"); err == nil || fk.overN.Load() != 1 {
		t.Fatalf("overpass 429: %v, calls %d", err, fk.overN.Load())
	}

	fk = newFake(t)
	f := fk.finder()
	f.wait = 0
	f.gate <- struct{}{}
	if _, err := f.Search(ctx, "3.0, 101.5", "3.1, 101.5", "fuel"); err != ErrBusy {
		t.Fatalf("busy: %v", err)
	}
}
