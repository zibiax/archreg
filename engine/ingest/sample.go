package ingest

import "fmt"

// Sample returns a small three-storey apartment building as raw loader
// output (z up, metres), with a few deliberate problems so every kind of
// result shows up: a bathroom too narrow to turn a wheelchair in, an escape
// window that's too small, a low ceiling, a narrow entrance door and no lift.
func Sample() (Raw, []float32) {
	var pos []float32
	raw := Raw{Source: "sample", FileName: "Exempelhus (sample)", UnitScale: 1, GeomScale: 1}
	add := func(id, kind, name string, min, max [3]float64, attrs map[string]any, storey string) {
		off := len(pos)
		pos = append(pos, box(min, max)...)
		raw.Elements = append(raw.Elements, RawElement{
			ID: id, Kind: kind, Name: name, Attrs: attrs, Storey: storey, Off: off, Len: len(pos) - off,
		})
	}

	const (
		w, d = 24.0, 12.0 // building width (x) and depth (y)
		fh   = 3.0        // floor to floor
	)
	for s := 0; s < 3; s++ {
		z := float64(s) * fh
		sid := fmt.Sprintf("storey-%d", s)
		raw.Elements = append(raw.Elements, RawElement{ID: sid, Kind: "storey", Name: fmt.Sprintf("Plan %d", s+1), Attrs: map[string]any{"Elevation": z}})
		add(fmt.Sprintf("slab-%d", s), "slab", "Bjälklag", [3]float64{0, 0, z}, [3]float64{w, d, z + 0.25}, nil, sid)
		// Outer walls.
		add(fmt.Sprintf("wall-s-%d", s), "wall", "Yttervägg", [3]float64{0, 0, z}, [3]float64{w, 0.3, z + fh}, nil, sid)
		add(fmt.Sprintf("wall-n-%d", s), "wall", "Yttervägg", [3]float64{0, d - 0.3, z}, [3]float64{w, d, z + fh}, nil, sid)
		add(fmt.Sprintf("wall-w-%d", s), "wall", "Yttervägg", [3]float64{0, 0, z}, [3]float64{0.3, d, z + fh}, nil, sid)
		add(fmt.Sprintf("wall-e-%d", s), "wall", "Yttervägg", [3]float64{w - 0.3, 0, z}, [3]float64{w, d, z + fh}, nil, sid)

		fz := z + 0.25
		ceil := 2.5
		if s == 2 {
			ceil = 2.3 // top floor: low ceiling (below the old 2.40 m reference)
		}
		add(fmt.Sprintf("R%d01", s+1), "space", "Vardagsrum", [3]float64{0.3, 0.3, fz}, [3]float64{8, 7, fz + ceil}, nil, sid)
		add(fmt.Sprintf("R%d02", s+1), "space", "Sovrum", [3]float64{8, 0.3, fz}, [3]float64{12, 4.5, fz + ceil}, nil, sid)
		add(fmt.Sprintf("R%d03", s+1), "space", "Kök", [3]float64{0.3, 7, fz}, [3]float64{6, d - 0.3, fz + ceil}, nil, sid)
		add(fmt.Sprintf("R%d04", s+1), "space", "Badrum", [3]float64{6, 9.5, fz}, [3]float64{7.2, d - 0.3, fz + 2.4}, nil, sid) // 1.2 m wide: no 1.30 m turning circle
		add(fmt.Sprintf("R%d05", s+1), "space", "WC", [3]float64{9.5, 10.5, fz}, [3]float64{10.7, d - 0.3, fz + 2.4}, nil, sid)
		add(fmt.Sprintf("R%d06", s+1), "space", "Hall", [3]float64{12, 0.3, fz}, [3]float64{14, d - 0.3, fz + ceil}, nil, sid)

		// Windows on the south facade.
		for i, x := range []float64{2, 5, 9.5} {
			ww, wh, sill := 1.2, 1.3, 0.8
			name := "Fönster"
			attrs := map[string]any{"OverallWidth": ww, "OverallHeight": wh, "IsExternal": true}
			if i == 2 {
				ww, wh, sill = 0.5, 0.6, 1.1
				name = "Fönster sovrum (utrymning)"
				attrs = map[string]any{"OverallWidth": ww, "OverallHeight": wh, "IsExternal": true, "escape": true}
			}
			if s == 0 && i == 0 {
				sill = 0.4 // low sill: child safety
				name = "Fönster vardagsrum (lågt)"
			}
			add(fmt.Sprintf("W%d%d", s+1, i+1), "window", name, [3]float64{x, 0, fz + sill}, [3]float64{x + ww, 0.3, fz + sill + wh}, attrs, sid)
		}
		// Interior doors.
		add(fmt.Sprintf("D%d1", s+1), "door", "Dörr sovrum", [3]float64{12, 2, fz}, [3]float64{12.1, 2.9, fz + 2.1},
			map[string]any{"OverallWidth": 0.9, "OverallHeight": 2.1, "IsExternal": false}, sid)
		add(fmt.Sprintf("D%d2", s+1), "door", "Dörr förråd", [3]float64{12, 6, fz}, [3]float64{12.1, 6.7, fz + 2.1},
			map[string]any{"OverallWidth": 0.7, "OverallHeight": 2.1, "IsExternal": false}, sid)
	}
	// Entrance (too narrow) and stair core.
	add("D01", "door", "Entrédörr", [3]float64{16, 0, 0.25}, [3]float64{16.78, 0.3, 2.35},
		map[string]any{"OverallWidth": 0.78, "OverallHeight": 2.1, "IsExternal": true}, "storey-0")
	add("roof", "roof", "Yttertak", [3]float64{-0.3, -0.3, 9}, [3]float64{w + 0.3, d + 0.3, 9.4}, nil, "")

	return raw, pos
}

// box returns 12 triangles (triangle soup) for an axis-aligned box.
func box(a, b [3]float64) []float32 {
	v := [8][3]float64{
		{a[0], a[1], a[2]}, {b[0], a[1], a[2]}, {b[0], b[1], a[2]}, {a[0], b[1], a[2]},
		{a[0], a[1], b[2]}, {b[0], a[1], b[2]}, {b[0], b[1], b[2]}, {a[0], b[1], b[2]},
	}
	faces := [][3]int{
		{0, 2, 1}, {0, 3, 2}, // bottom (normal -z)
		{4, 5, 6}, {4, 6, 7}, // top
		{0, 1, 5}, {0, 5, 4}, // south
		{2, 3, 7}, {2, 7, 6}, // north
		{1, 2, 6}, {1, 6, 5}, // east
		{3, 0, 4}, {3, 4, 7}, // west
	}
	out := make([]float32, 0, 12*9)
	for _, f := range faces {
		for _, i := range f {
			out = append(out, float32(v[i][0]), float32(v[i][1]), float32(v[i][2]))
		}
	}
	return out
}
