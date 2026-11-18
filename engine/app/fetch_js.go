//go:build js && wasm

package app

import (
	"errors"
	"fmt"
	"syscall/js"
)

// httpGet uses the browser's fetch directly; net/http would add megabytes
// to the WASM file. Must be called from a goroutine, not the JS event loop.
func httpGet(u string) ([]byte, error) {
	type res struct {
		b   []byte
		err error
	}
	ch := make(chan res, 1)
	var onResp, onBuf, onErr js.Func
	release := func() { onResp.Release(); onBuf.Release(); onErr.Release() }
	onErr = js.FuncOf(func(this js.Value, args []js.Value) any {
		msg := "fetch failed"
		if len(args) > 0 {
			msg = args[0].Call("toString").String()
		}
		ch <- res{err: errors.New(msg)}
		return nil
	})
	onBuf = js.FuncOf(func(this js.Value, args []js.Value) any {
		u8 := js.Global().Get("Uint8Array").New(args[0])
		b := make([]byte, u8.Get("length").Int())
		js.CopyBytesToGo(b, u8)
		ch <- res{b: b}
		return nil
	})
	onResp = js.FuncOf(func(this js.Value, args []js.Value) any {
		r := args[0]
		if !r.Get("ok").Bool() {
			ch <- res{err: fmt.Errorf("%s: HTTP %d", u, r.Get("status").Int())}
			return nil
		}
		r.Call("arrayBuffer").Call("then", onBuf, onErr)
		return nil
	})
	js.Global().Call("fetch", u).Call("then", onResp, onErr)
	r := <-ch
	release()
	return r.b, r.err
}
