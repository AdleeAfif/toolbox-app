package stops

import (
	"math"
	"regexp"
	"strconv"
)

const (
	earthMeters  = 6371000.0
	metersPerDeg = earthMeters * math.Pi / 180
)

// Point is a WGS84 coordinate.
type Point struct {
	Lat, Lng float64
}

var coordRe = regexp.MustCompile(`^(-?\d+(?:\.\d+)?)\s*,\s*(-?\d+(?:\.\d+)?)$`)

// parseCoords reads "lat, lng". Anything else, or a value out of range, is not coordinates.
func parseCoords(s string) (Point, bool) {
	m := coordRe.FindStringSubmatch(s)
	if m == nil {
		return Point{}, false
	}
	lat, err1 := strconv.ParseFloat(m[1], 64)
	lng, err2 := strconv.ParseFloat(m[2], 64)
	if err1 != nil || err2 != nil || lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return Point{}, false
	}
	return Point{Lat: lat, Lng: lng}, true
}

func haversine(a, b Point) float64 {
	la1, la2 := a.Lat*math.Pi/180, b.Lat*math.Pi/180
	dLat := la2 - la1
	dLng := (b.Lng - a.Lng) * math.Pi / 180
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(la1)*math.Cos(la2)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earthMeters * math.Asin(math.Min(1, math.Sqrt(h)))
}

// route is the driving line. motorway[i] describes the segment pts[i] to pts[i+1],
// and cum[i] is the distance along the route to pts[i] in meters.
type route struct {
	pts      []Point
	motorway []bool
	cum      []float64
	meters   float64
}

func newRoute(pts []Point, motorway []bool, meters float64) route {
	cum := make([]float64, len(pts))
	for i := 1; i < len(pts); i++ {
		cum[i] = cum[i-1] + haversine(pts[i-1], pts[i])
	}
	return route{pts: pts, motorway: motorway, cum: cum, meters: meters}
}

// nearest finds the closest point on the route to p. It returns the segment, the
// distance along the route, the distance off it, and the cross product of the
// segment direction with p, which is positive when p is on the left.
func (r route) nearest(p Point) (seg int, along, off, cross float64) {
	cosLat := math.Cos(p.Lat * math.Pi / 180)
	off = math.Inf(1)
	for i := 0; i+1 < len(r.pts); i++ {
		ax, ay := (r.pts[i].Lng-p.Lng)*cosLat*metersPerDeg, (r.pts[i].Lat-p.Lat)*metersPerDeg
		bx, by := (r.pts[i+1].Lng-p.Lng)*cosLat*metersPerDeg, (r.pts[i+1].Lat-p.Lat)*metersPerDeg
		dx, dy := bx-ax, by-ay
		t := 0.0
		if l2 := dx*dx + dy*dy; l2 > 0 {
			t = math.Max(0, math.Min(1, -(ax*dx+ay*dy)/l2))
		}
		if d := math.Hypot(ax+t*dx, ay+t*dy); d < off {
			off = d
			seg = i
			along = r.cum[i] + t*(r.cum[i+1]-r.cum[i])
			cross = dy*ax - dx*ay
		}
	}
	return seg, along, off, cross
}

// simplify drops points within tol meters of the line, doubling tol until at
// most max points remain. The first and last points are always kept.
func simplify(pts []Point, tol float64, max int) []Point {
	if len(pts) <= 2 {
		return pts
	}
	for {
		out := douglasPeucker(pts, tol)
		if len(out) <= max {
			return out
		}
		tol *= 2
	}
}

func douglasPeucker(pts []Point, tol float64) []Point {
	cosLat := math.Cos(pts[0].Lat * math.Pi / 180)
	x := func(p Point) float64 { return p.Lng * cosLat * metersPerDeg }
	y := func(p Point) float64 { return p.Lat * metersPerDeg }

	last := len(pts) - 1
	keep := make([]bool, len(pts))
	keep[0], keep[last] = true, true
	stack := [][2]int{{0, last}}
	for len(stack) > 0 {
		s, e := stack[len(stack)-1][0], stack[len(stack)-1][1]
		stack = stack[:len(stack)-1]
		if e-s < 2 {
			continue
		}
		far, idx := 0.0, -1
		for i := s + 1; i < e; i++ {
			if d := segDist(x(pts[i]), y(pts[i]), x(pts[s]), y(pts[s]), x(pts[e]), y(pts[e])); d > far {
				far, idx = d, i
			}
		}
		if far > tol {
			keep[idx] = true
			stack = append(stack, [2]int{s, idx}, [2]int{idx, e})
		}
	}
	out := make([]Point, 0, len(pts))
	for i, k := range keep {
		if k {
			out = append(out, pts[i])
		}
	}
	return out
}

func segDist(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	t := 0.0
	if l2 := dx*dx + dy*dy; l2 > 0 {
		t = math.Max(0, math.Min(1, ((px-ax)*dx+(py-ay)*dy)/l2))
	}
	return math.Hypot(px-(ax+t*dx), py-(ay+t*dy))
}

// inPolygon is a ray-casting test on lat/lng, fine at the size of a service area.
func inPolygon(p Point, poly []Point) bool {
	in := false
	for i, j := 0, len(poly)-1; i < len(poly); j, i = i, i+1 {
		a, b := poly[i], poly[j]
		if (a.Lat > p.Lat) != (b.Lat > p.Lat) &&
			p.Lng < (b.Lng-a.Lng)*(p.Lat-a.Lat)/(b.Lat-a.Lat)+a.Lng {
			in = !in
		}
	}
	return in
}
