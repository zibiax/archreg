//go:build js && wasm

// Command wasm is the whole app compiled to WebAssembly. It exposes
// globalThis.archreg to the JS glue (web/app.js). Every call returns a
// Promise of a JSON-encoded app.View (except sample() and check()).
package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"syscall/js"

	"github.com/martin/archreg/engine/app"
	"github.com/martin/archreg/engine/i18n"
	"github.com/martin/archreg/engine/ingest"
	"github.com/martin/archreg/engine/rules"
	ruleset "github.com/martin/archreg/rules"
)

var a *app.App

func main() {
	api := js.Global().Get("Object").New()
	api.Set("version", rules.Version)

	// init(lang) — "sv" or "en"
	api.Set("init", js.FuncOf(func(this js.Value, args []js.Value) any {
		base := js.Global().Get("location").Get("href").String()
		lang := str(args, 0)
		return promise(func() (any, error) {
			var err error
			a, err = app.New(base, ruleset.Boverket2026)
			if err != nil {
				return nil, err
			}
			a.Lang = i18n.Parse(lang)
			return viewJSON(a.Init())
		})
	}))
	api.Set("dropPin", js.FuncOf(func(this js.Value, args []js.Value) any {
		lon, lat := args[0].Float(), args[1].Float()
		return promise(func() (any, error) { return viewJSON(a.DropPin(lon, lat)) })
	}))
	api.Set("viewport", js.FuncOf(func(this js.Value, args []js.Value) any {
		v := [5]float64{}
		for i := range v {
			v[i] = args[i].Float()
		}
		return promise(func() (any, error) { return viewJSON(a.Viewport(v[0], v[1], v[2], v[3], v[4])) })
	}))
	// search(q, biasLon, biasLat) → View
	api.Set("search", js.FuncOf(func(this js.Value, args []js.Value) any {
		q, lon, lat := str(args, 0), num(args, 1), num(args, 2)
		return promise(func() (any, error) { return viewJSON(a.Search(q, lon, lat)) })
	}))
	// suggest(q, biasLon, biasLat) → JSON array of app.Place
	api.Set("suggest", js.FuncOf(func(this js.Value, args []js.Value) any {
		q, lon, lat := str(args, 0), num(args, 1), num(args, 2)
		return promise(func() (any, error) {
			ps, err := app.Suggest(q, lon, lat)
			if err != nil {
				return nil, err
			}
			if ps == nil {
				ps = []app.Place{}
			}
			b, err := json.Marshal(ps)
			return string(b), err
		})
	}))
	// goTo(lon, lat, zoom, label) → View
	api.Set("goTo", js.FuncOf(func(this js.Value, args []js.Value) any {
		lon, lat, zoom, label := num(args, 0), num(args, 1), num(args, 2), str(args, 3)
		return promise(func() (any, error) { return viewJSON(a.GoTo(lon, lat, zoom, label)) })
	}))
	api.Set("moveModel", js.FuncOf(func(this js.Value, args []js.Value) any {
		lon, lat := args[0].Float(), args[1].Float()
		return promise(func() (any, error) { return viewJSON(a.MoveModel(lon, lat)) })
	}))
	api.Set("action", js.FuncOf(func(this js.Value, args []js.Value) any {
		name, value, id := str(args, 0), str(args, 1), str(args, 2)
		return promise(func() (any, error) { return viewJSON(a.Action(name, value, id)) })
	}))
	// loadModel(rawJSON: string, positions: Uint8Array view of a Float32Array)
	api.Set("loadModel", js.FuncOf(func(this js.Value, args []js.Value) any {
		raw := []byte(args[0].String())
		buf := make([]byte, args[1].Get("byteLength").Int())
		js.CopyBytesToGo(buf, args[1])
		return promise(func() (any, error) {
			pos := make([]float32, len(buf)/4)
			for i := range pos {
				pos[i] = math.Float32frombits(binary.LittleEndian.Uint32(buf[i*4:]))
			}
			return viewJSON(a.LoadModel(raw, pos))
		})
	}))
	// sample() → {raw: string, positions: Uint8Array}
	api.Set("sample", js.FuncOf(func(this js.Value, args []js.Value) any {
		raw, pos := ingest.Sample()
		rj, _ := json.Marshal(raw)
		b := make([]byte, len(pos)*4)
		for i, f := range pos {
			binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(f))
		}
		u8 := js.Global().Get("Uint8Array").New(len(b))
		js.CopyBytesToJS(u8, b)
		o := js.Global().Get("Object").New()
		o.Set("raw", string(rj))
		o.Set("positions", u8)
		return o
	}))
	// check(inputJSON, rulesJSON?) → result JSON (the pure engine, for testing).
	api.Set("check", js.FuncOf(func(this js.Value, args []js.Value) any {
		rs := ruleset.Boverket2026
		if len(args) > 1 && args[1].Type() == js.TypeString {
			rs = []byte(args[1].String())
		}
		out, err := rules.CheckJSON([]byte(str(args, 0)), rs)
		if err != nil {
			b, _ := json.Marshal(map[string]string{"error": err.Error()})
			return string(b)
		}
		return string(out)
	}))

	js.Global().Set("archreg", api)
	if cb := js.Global().Get("__archregReady"); cb.Type() == js.TypeFunction {
		cb.Invoke()
	}
	select {}
}

func str(args []js.Value, i int) string {
	if i < len(args) && args[i].Type() == js.TypeString {
		return args[i].String()
	}
	return ""
}

func num(args []js.Value, i int) float64 {
	if i < len(args) && args[i].Type() == js.TypeNumber {
		return args[i].Float()
	}
	return 0
}

func viewJSON(v *app.View) (any, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

// promise runs fn in a goroutine (so it may block on fetch) and returns a JS
// Promise of its result.
func promise(fn func() (any, error)) js.Value {
	var handler js.Func
	handler = js.FuncOf(func(this js.Value, args []js.Value) any {
		resolve, reject := args[0], args[1]
		go func() {
			defer handler.Release()
			defer func() {
				if r := recover(); r != nil {
					reject.Invoke(js.Global().Get("Error").New(fmt.Sprint("archreg: ", r)))
				}
			}()
			v, err := fn()
			if err != nil {
				reject.Invoke(js.Global().Get("Error").New(err.Error()))
				return
			}
			resolve.Invoke(v)
		}()
		return nil
	})
	return js.Global().Get("Promise").New(handler)
}
