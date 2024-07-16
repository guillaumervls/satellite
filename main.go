package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strconv"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/mitchellh/mapstructure"
)

const (
	MaxCacheSize     = 1000
	MaxSatteliteSize = 128 * 1024 // 128KB
)

var cache, cacheCreationErr = lru.New[string, *vm.Program](MaxCacheSize)

func main() {
	if cacheCreationErr != nil {
		panic(fmt.Errorf("error creating cache: %v", cacheCreationErr))
	}

	// Only listen on 127.0.0.1 if the --local-only flag is present.
	// Useful for development: e.g. MacOS will pop up a dialog asking to allow incoming connections on every (re)start.
	addr := ":8080"
	for _, arg := range os.Args[1:] {
		if arg == "--local-only" {
			addr = "127.0.0.1:8080"
			break
		}
	}

	server := &http.Server{
		Addr:    addr,
		Handler: http.HandlerFunc(handler),
	}

	server.ListenAndServe()
}

type SatelliteEnv struct {
	Method string
	Header http.Header
	URL    url.URL
}

type SatelliteResult struct {
	StatusCode int
	Header     http.Header
	Body       string
}

func handler(w http.ResponseWriter, r *http.Request) {
	satLocation := r.Header.Get("X-Satellite-Location")
	if satLocation == "" {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "no X-Satellite-Location header found")
		return
	}

	satellite, ok := cache.Get(satLocation)
	if !ok {
		satRes, err := http.Get(satLocation)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "error fetching satellite: %v", err)
			return
		}
		satSize, err := strconv.Atoi(satRes.Header.Get("Content-Length"))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "error reading satellite size (Content-Length header): %v", err)
			return
		}
		if satSize > MaxSatteliteSize {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "satellite too large: max %v, got %v", MaxSatteliteSize, satSize)
			return
		}
		satBytes, err := io.ReadAll(satRes.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "error reading satellite: %v", err)
			return
		}
		satCode := string(satBytes)
		if satRes.StatusCode != http.StatusOK {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "http error %v fetching satellite: %v", satRes.StatusCode, satCode)
			return
		}
		satellite, err = expr.Compile(satCode, expr.Env(SatelliteEnv{}), expr.AsKind(reflect.Map))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "error compiling satellite: %v", err)
			return
		}
		cache.Add(satLocation, satellite)
	}

	rawOut, err := expr.Run(satellite, SatelliteEnv{
		Header: r.Header,
		Method: r.Method,
		URL:    *r.URL,
	})
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "error running satellite: %v", err)
		return
	}
	out := SatelliteResult{}
	err = mapstructure.Decode(rawOut, &out)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "error decoding satellite result: %v", err)
		return
	}
	if out.StatusCode == 0 {
		out.StatusCode = http.StatusOK
	}
	if http.StatusText(out.StatusCode) == "" {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "error in satellite result: invalid status code: %v", out.StatusCode)
		return
	}

	for k, v := range out.Header {
		for _, vv := range v {
			w.Header().Add(k, vv)
		}
	}
	w.WriteHeader(out.StatusCode)
	w.Write([]byte(out.Body))
}
