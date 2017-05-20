package main

import (
	"fmt"
	"net/http"
	"net/http/httputil"
)

func printReqHandler(res http.ResponseWriter, req *http.Request) {
	reqDump, err := httputil.DumpRequest(req, true)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println(string(reqDump))
}

func registerHandler(path string, handlerFunc func(http.ResponseWriter, *http.Request)) {
	if path != "" {
		fmt.Printf("Registering handler for %s", path)
		fmt.Println()
		http.HandleFunc(path, handlerFunc)
	}
}

func main() {
	fmt.Println("gosigsrv starting")
	fmt.Println()
	// Register handlers
	registerHandler("/", printReqHandler)

	// Start listening
	http.ListenAndServe(":8087", nil)
	fmt.Println()
	fmt.Println("gosigsrv exiting")
}
