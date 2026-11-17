//go:build js && wasm

// Command wasm exposes the rules engine to JavaScript as
//
//	globalThis.archreg.check(inputJSON: string, rulesJSON?: string): string
//
// The returned string is JSON: either an Output or {"error": "..."}.
package main

import (
	"encoding/json"
	"syscall/js"

	"github.com/martin/archreg/engine/rules"
)

func main() {
	api := js.Global().Get("Object").New()
	api.Set("version", rules.Version)
	api.Set("check", js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) < 1 {
			return errJSON("check(inputJSON, rulesJSON?) needs at least one argument")
		}
		var rulesJSON []byte
		if len(args) > 1 && args[1].Type() == js.TypeString {
			rulesJSON = []byte(args[1].String())
		}
		out, err := rules.CheckJSON([]byte(args[0].String()), rulesJSON)
		if err != nil {
			return errJSON(err.Error())
		}
		return string(out)
	}))
	js.Global().Set("archreg", api)
	if cb := js.Global().Get("__archregReady"); cb.Type() == js.TypeFunction {
		cb.Invoke()
	}
	select {} // keep the Go runtime alive
}

func errJSON(msg string) string {
	b, _ := json.Marshal(map[string]string{"error": msg})
	return string(b)
}
