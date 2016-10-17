package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"golang.org/x/net/context"
	"golang.org/x/net/trace"
)

type envelope struct {
	Okay  bool        `json:"okay"`
	Error string      `json:"error"`
	Data  interface{} `json:"data"`
}

type apiHandler func(context.Context, *http.Request) (interface{}, error)

func tracePrint(ctx context.Context, f string, a ...interface{}) {
	if tr, ok := trace.FromContext(ctx); ok {
		tr.LazyPrintf(f, a...)
	}
}

func handleAPI(endpoint string, handler apiHandler) {
	address := fmt.Sprintf("/api/1/%s", endpoint)
	http.HandleFunc(address, func(w http.ResponseWriter, r *http.Request) {
		tr := trace.New("webled.api", r.URL.String())
		tr.LazyPrintf("API request from %s", r.RemoteAddr)
		ctx := trace.NewContext(context.Background(), tr)

		e := envelope{}
		data, err := handler(ctx, r)
		if err != nil {
			tr.LazyPrintf("Handler returned eror: %v", err)
			tr.SetError()
			w.WriteHeader(503)
			e.Okay = false
			e.Error = err.Error()
		} else {
			tr.LazyPrintf("Handler finished succesfully.")
			e.Okay = true
			e.Error = "No error."
			e.Data = data
		}
		bytes, err := json.Marshal(e)
		if err != nil {
			tr.LazyPrintf("Could not marshal response: %v", err)
			tr.SetError()
			tr.Finish()
			w.WriteHeader(500)
			return
		}
		w.Write(bytes)
		tr.LazyPrintf("Sent response: %d bytes.", len(bytes))
		tr.Finish()
	})
}
