package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httputil"
	"os"
	"strings"

	"sync"
)

type peerKind int

const (
	client peerKind = iota
	server
)

type peerInfo struct {
	Kind    peerKind
	Name    string
	ID      string
	Channel chan string
}

const peerIDParamName string = "peer_id"
const toParamName string = "to"

const peerMessageBufferSize int = 100

var peers = make(map[string]peerInfo)

var peerIDCount uint
var peerMutex sync.Mutex

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

func setConnectionHeader(header http.Header, close bool) {
	if close {
		header.Set("Connection", "close")
	} else {
		header.Set("Connection", "keep-alive")
	}
}

func setVersionHeader(header http.Header) {
	header.Set("Server", "PeerConnectionTestServer/0.1g")
}

func setNoCacheHeader(header http.Header) {
	header.Set("Cache-Control", "no-cache")
}

func addCorsHeaders(header http.Header) {
	header.Set("Access-Control-Allow-Origin", "*")
	header.Set("Access-Control-Allow-Credentials", "true")
	header.Set("Access-Control-Allow-Methods", strings.Join([]string{"GET", "POST", "OPTIONS"}, ","))
	header.Set("Access-Control-Allow-Headers", strings.Join([]string{"Content-Type", "Content-Length", "Cache-Control", "Connection"}, ","))
	header.Set("Access-Control-Expose-Headers", strings.Join([]string{"Content-Length", "X-Peer-Id"}, ","))
}

func addPragmaHeader(header http.Header, peerID string) {
	header.Set("Pragma", peerID)
}

func addCommonHeaders(header http.Header, closeConnection bool) {
	setConnectionHeader(header, closeConnection)
	setNoCacheHeader(header)
	setVersionHeader(header)
	addCorsHeaders(header)
}

func signinHandler(res http.ResponseWriter, req *http.Request) {
	addCommonHeaders(res.Header(), true)

	if req.Method != "GET" {
		http.Error(res, "Bad request", http.StatusBadRequest)
		return
	}
	var name string
	// Parse out peer name
	for k, v := range req.URL.Query() {
		if v[0] == "" {
			name = k
			break
		}
	}

	if name == "" {
		http.Error(res, "No name", http.StatusBadRequest)
		return
	}

	var peerInfo peerInfo
	peerInfo.Name = name
	peerInfo.Channel = make(chan string, peerMessageBufferSize)

	// Determine peer type
	if strings.Index(name, "renderingserver_") == 0 {
		peerInfo.Kind = server
	}

	// Generate id
	peerMutex.Lock()
	peerIDCount++
	peerInfo.ID = fmt.Sprintf("%d", peerIDCount)
	peerMutex.Unlock()

	peers[peerInfo.ID] = peerInfo

	addPragmaHeader(res.Header(), peerInfo.ID)

	peerInfoString := fmt.Sprintf("%s,%s,1", peerInfo.Name, peerInfo.ID)
	peerInfoString += fmt.Sprintln()
	responseString := peerInfoString

	// Return above + current peers (filtered for oppositing type)
	for pID, pInfo := range peers {
		if pID != peerInfo.ID && pInfo.Kind != peerInfo.Kind {
			responseString += fmt.Sprintf("%s,%s,1", pInfo.Name, pInfo.ID)
			responseString += fmt.Sprintln()

			// Also notify these peers that the new one exists
			if len(pInfo.Channel) < cap(pInfo.Channel) {
				pInfo.Channel <- peerInfoString
			} else {
				// TODO: Figure out what to do when peeer message buffer fills up
			}
		}
	}
	res.WriteHeader(http.StatusOK)
	fmt.Fprintf(res, responseString)
	// http.Error(res, "Not implemented "+name+" "+uuid.String(), http.statusadd)
}

func signoutHandler(res http.ResponseWriter, req *http.Request) {
	if req.Method != "GET" {
		http.Error(res, "Bad request", http.StatusBadRequest)
		return
	}
	addCommonHeaders(res.Header(), true)
	var peerID string
	// Parse out peers id
	for k, v := range req.URL.Query() {
		if k == peerIDParamName {
			peerID = v[0]
		}
	}
	_, exists := peers[peerID]
	if !exists {
		http.Error(res, "Unknown peer", http.StatusBadRequest)
		return
	}
	addPragmaHeader(res.Header(), peerID)
	delete(peers, peerID)
	res.WriteHeader(http.StatusOK)
}

func messageHandler(res http.ResponseWriter, req *http.Request) {
	if req.Method != "POST" {
		http.Error(res, "Bad request", http.StatusBadRequest)
		return
	}

	// Parse out from id
	// Parse out to id
	peerID, peerExists := req.URL.Query()[peerIDParamName]
	toID, toExists := req.URL.Query()[toParamName]

	if !peerExists || !toExists {
		http.Error(res, "Missing Peer or To ID", http.StatusBadRequest)
		return
	}

	_, peerInfoExists := peers[peerID[0]]
	to, toInfoExists := peers[toID[0]]

	if !peerInfoExists || !toInfoExists {
		http.Error(res, "Invalid Peer or To ID", http.StatusBadRequest)
		return
	}

	addPragmaHeader(res.Header(), peerID[0])

	requestData, err := ioutil.ReadAll(req.Body)
	if err != nil {
		http.Error(res, err.Error(), http.StatusInternalServerError)
	}
	requestString := string(requestData)
	defer req.Body.Close()
	// Look up channel for to id
	if len(to.Channel) == cap(to.Channel) {
		http.Error(res, "Peer is backed up", http.StatusServiceUnavailable)
		return
	}
	to.Channel <- requestString

	// Send message to channel for to id
	res.WriteHeader(http.StatusOK)
}

func waitHandler(res http.ResponseWriter, req *http.Request) {
	addCommonHeaders(res.Header(), true)

	if req.Method != "GET" {
		http.Error(res, "Bad request", http.StatusBadRequest)
		return
	}

	// Parse out peer id
	peerID, peerExists := req.URL.Query()[peerIDParamName]

	if !peerExists {
		http.Error(res, "Missing Peer ID", http.StatusBadRequest)
		return
	}

	peerInfo, peerInfoExists := peers[peerID[0]]

	if !peerInfoExists {
		http.Error(res, "Unknown peer", http.StatusBadRequest)
		return
	}

	addPragmaHeader(res.Header(), peerID[0])

	// Look up message channel for peers id
	// Wait for message to reply
	responseString := <-peerInfo.Channel

	res.WriteHeader(http.StatusOK)
	fmt.Fprint(res, responseString)
}

func main() {

	fmt.Println("gosigsrv starting")
	fmt.Println()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8087"
	}

	fmt.Printf("Will listen on port %s\n\n", port)

	// Register handlers
	registerHandler("/sign_in", signinHandler)
	registerHandler("/sign_out", signoutHandler)
	registerHandler("/message", messageHandler)
	registerHandler("/wait", waitHandler)
	registerHandler("/", printReqHandler)

	// Start listening
	http.ListenAndServe(fmt.Sprintf(":%s", port), nil)
	fmt.Println()
	fmt.Println("gosigsrv exiting")
}
