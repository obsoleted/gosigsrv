package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httputil"
	"os"
	"strings"
	"sync"
	"time"
)

type peerKind int

const (
	client peerKind = iota
	server
)

type peerMsg struct {
	FromID  string
	Message string
}

type peerInfo struct {
	Kind          peerKind
	Name          string
	ID            string
	Channel       chan *peerMsg
	ConnectedWith string
	LastContact   time.Time
	Waiting       bool
}

func (m peerInfo) String() string {
	return fmt.Sprintf("%s@%s[%s]", m.Name, m.ID, m.ConnectedWith)
}

func (m peerInfo) InfoString() string {
	return fmt.Sprintf("%s,%s,1\n", m.Name, m.ID)
}

const peerIDParamName string = "peer_id"
const toParamName string = "to"

const peerMessageBufferSize int = 100

var peers = make(map[string]*peerInfo)

var peerIDCount uint
var peerMutex sync.Mutex

func printReqHandler(res http.ResponseWriter, req *http.Request) {
	reqDump, err := httputil.DumpRequest(req, true)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println(string(reqDump))
}

func registerHandler(path string, handlerFunc http.Handler) {
	if path != "" {
		fmt.Printf("Registering handler for %s", path)
		fmt.Println()
		http.Handle(path, handlerFunc)
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

func setPragmaHeader(header http.Header, peerID string) {
	header.Set("Pragma", peerID)
}

// printStats prints out the current peer count and count by type
func printStats() {
	var serverCount int
	var clientCount int
	for _, v := range peers {
		if v.Kind == server {
			serverCount++
		} else {
			clientCount++
		}
	}
	fmt.Printf("TotalPeers: %d, Servers: %d, Clients: %d\n", len(peers), serverCount, clientCount)
}

// commonHeaderMiddleware sets the common headers that all responses seem to require
func commonHeaderMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		setNoCacheHeader(res.Header())
		setVersionHeader(res.Header())
		addCorsHeaders(res.Header())
		setConnectionHeader(res.Header(), true)
		next.ServeHTTP(res, req)
	})
}

// signinHandler handles the sign in requests
//
//   It takes the first parameter with no value as the client name
//   and assigns it the next peer id (just an increasing int for now)
func signinHandler(res http.ResponseWriter, req *http.Request) {

	if req.Method != "GET" {
		http.Error(res, "Bad request", http.StatusBadRequest)
		return
	}

	// Parse out peer name
	var name string
	for k, v := range req.URL.Query() {
		// Pick the first query param without a value
		//  e.g. /sign_in?notname=foo&name should pick 'name'
		if v[0] == "" {
			name = k
			break
		}
	}

	if name == "" {
		http.Error(res, "No name", http.StatusBadRequest)
		return
	}

	// Create and populate new peer info struct
	var peerInfo peerInfo
	peerInfo.Name = name
	peerInfo.Channel = make(chan *peerMsg, peerMessageBufferSize)
	peerInfo.LastContact = time.Now().UTC()

	// Determine peer type
	if strings.Index(name, "renderingserver_") == 0 {
		peerInfo.Kind = server
	}

	// Generate id
	peerMutex.Lock()
	peerIDCount++
	peerInfo.ID = fmt.Sprintf("%d", peerIDCount)
	peerMutex.Unlock()

	// Add to peer map
	// TOOD: Guard this with mutex?
	peers[peerInfo.ID] = &peerInfo

	// Build up response string:
	//   new peer info string
	peerInfoString := peerInfo.InfoString()
	responseString := peerInfoString

	//   current peers (filtered for oppositing type and only peers w/o connections
	for pID, pInfo := range peers {
		if pInfo == nil {
			fmt.Printf("ERROR: nil peer found at id %s\n", pID)
			continue
		}

		if pID != peerInfo.ID && pInfo.Kind != peerInfo.Kind && pInfo.ConnectedWith == "" {
			responseString += pInfo.InfoString()

			// Also notify these peers that the new one exists
			if len(pInfo.Channel) < cap(pInfo.Channel) {
				pInfo.Channel <- &peerMsg{pInfo.ID, peerInfoString}
			} else {
				fmt.Printf("WARNING: Dropped message for peer %s", pInfo)
				// TODO: Figure out what to do when peeer message buffer fills up
			}
		}
	}

	// Set header to match new peer id
	setPragmaHeader(res.Header(), peerInfo.ID)

	res.Header().Set("Content-Length", fmt.Sprintf("%d", len(responseString)))
	// Set status code
	res.WriteHeader(http.StatusOK)

	// Write response content
	_, err := fmt.Fprintf(res, responseString)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
	}
	fmt.Printf("sign-in - Peer: %s\n", peerInfo)
	printStats()
}

func signoutHandler(res http.ResponseWriter, req *http.Request) {
	if req.Method != "GET" {
		http.Error(res, "Bad request", http.StatusBadRequest)
		return
	}
	var peerID string
	// Parse out peers id
	peerIDValues, peerIDExists := req.URL.Query()[peerIDParamName]
	if peerIDExists {
		peerID = peerIDValues[0]
	}

	peer, exists := peers[peerID]
	if !exists || peer == nil {
		http.Error(res, "Unknown peer", http.StatusBadRequest)
		return
	}

	if peer.ConnectedWith != "" {
		connectedPeer, connectionExists := peers[peer.ConnectedWith]
		if connectionExists && connectedPeer != nil {
			connectedPeer.ConnectedWith = ""
		}
	}

	setPragmaHeader(res.Header(), peerID)
	delete(peers, peerID)
	res.WriteHeader(http.StatusOK)

	fmt.Printf("sign-out - Peer: %s\n", peer)
	printStats()
}

// messageHandler handles requests from a peer to send a message to another peer
func messageHandler(res http.ResponseWriter, req *http.Request) {
	if req.Method != "POST" {
		http.Error(res, "Bad request", http.StatusBadRequest)
		return
	}

	// Parse out from id
	// Parse out to id
	peerIDValues, peerExists := req.URL.Query()[peerIDParamName]
	toIDValues, toExists := req.URL.Query()[toParamName]

	if !peerExists || !toExists {
		http.Error(res, "Missing Peer or To ID", http.StatusBadRequest)
		return
	}

	peerID := peerIDValues[0]
	toID := toIDValues[0]

	from, peerInfoExists := peers[peerID]
	to, toInfoExists := peers[toID]

	if !peerInfoExists || !toInfoExists || from == nil || to == nil {
		http.Error(res, "Invalid Peer or To ID", http.StatusBadRequest)
		return
	}
	// Update the last time we heard from peer
	from.LastContact = time.Now().UTC()

	if from.ConnectedWith == "" {
		fmt.Printf("Connecting %s with %s\n", from, to)
		from.ConnectedWith = to.ID
	}

	if to.ConnectedWith == "" {
		fmt.Printf("Connecting %s with %s\n", to, from)
		to.ConnectedWith = from.ID
	}

	if from.ConnectedWith != to.ID {
		fmt.Printf("WARNING: Peer sending message to recipient outside room\n")
	}

	// Must set pragma to peer id of sender
	setPragmaHeader(res.Header(), peerID)

	// Read message data as a string and send it to the recipients channel
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
	// channel gets message + sender id
	to.Channel <- &peerMsg{peerID, requestString}

	res.WriteHeader(http.StatusOK)
	fmt.Printf("message: %s -> %s: \n\t%s\n", from, to, requestString)
}

// waitHandler handles requests from clients looking for meesages
//
//   Clients seem to use this in a hanging get/polling situation
func waitHandler(res http.ResponseWriter, req *http.Request) {

	if req.Method != "GET" {
		http.Error(res, "Bad request", http.StatusBadRequest)
		return
	}

	// Parse out peer id
	peerIDValues, peerExists := req.URL.Query()[peerIDParamName]

	if !peerExists {
		http.Error(res, "Missing Peer ID", http.StatusBadRequest)
		return
	}

	peerID := peerIDValues[0]

	peerInfo, peerInfoExists := peers[peerID]

	if !peerInfoExists || peerInfo == nil {
		http.Error(res, "Unknown peer", http.StatusBadRequest)
		return
	}

	// Update the last time we heard from peer
	peerInfo.LastContact = time.Now().UTC()
	// Also set that peer is waiting (so that peer isn't cleaned up)
	peerInfo.Waiting = true

	fmt.Printf("wait: Peer %s waiting...\n", peerInfo)

	// Wait for message (from channel) OR client disconnect
	var peerMsg *peerMsg
	var cancelled bool
	select {
	case peerMsg = <-(peerInfo.Channel):
	case <-req.Context().Done():
		cancelled = true
	}
	peerInfo.Waiting = false

	if cancelled {
		fmt.Printf("Peer (%s) cancelled/closed connection. Terminating wait call.\n", peerInfo)
		return
	}
	if peerMsg == nil {
		fmt.Printf("Error: nil peerMsg in channel")
		http.Error(res, "Bad message", http.StatusInternalServerError)
		return
	}
	// It may have been some time since the msg came through so update the time
	peerInfo.LastContact = time.Now().UTC()

	res.Header().Set("Content-Length", fmt.Sprintf("%d", len(peerMsg.Message)))
	// Pragma must be set to the message *sender's* id
	setPragmaHeader(res.Header(), peerMsg.FromID)

	// set status and write out message contant to response
	res.WriteHeader(http.StatusOK)
	_, err := fmt.Fprint(res, peerMsg.Message)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
	}

	fmt.Printf("wait: Peer %s recieved message from ID %s\n\t%s\n\n", peerInfo, peerMsg.FromID, peerMsg.Message)
}

// peerCleanupRoutine periodically cleans up stale peers
//
//   Currently hardcoded to check every 30 seconds for peers
//   that haven't contacted the server in a minute or more
func peerCleanupRoutine() {
	tickerChan := time.NewTicker(time.Second * 30).C

	for {
		<-tickerChan
		fmt.Printf("Checking for stale peers\n")
		printStats()
		for k, v := range peers {
			if v == nil {
				fmt.Println("ERROR: nil peer in peers!")
				continue
			}
			if !v.Waiting && (time.Now().UTC().Sub(v.LastContact) > time.Minute*1) {
				fmt.Printf("Removing stale peer %s\n", v)
				connectedWithPeer := peers[v.ConnectedWith]
				if connectedWithPeer != nil {
					fmt.Printf("Disconnecting peer %s with id %s\n", v, connectedWithPeer)
					connectedWithPeer.ConnectedWith = ""
				}
				delete(peers, k)
			}
		}
	}
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
	registerHandler("/sign_in", commonHeaderMiddleware(http.HandlerFunc(signinHandler)))
	registerHandler("/sign_out", commonHeaderMiddleware(http.HandlerFunc(signoutHandler)))
	registerHandler("/message", commonHeaderMiddleware(http.HandlerFunc(messageHandler)))
	registerHandler("/wait", commonHeaderMiddleware(http.HandlerFunc(waitHandler)))
	registerHandler("/", commonHeaderMiddleware(http.HandlerFunc(printReqHandler)))

	// Start peer cleenup timer routine
	go peerCleanupRoutine()

	// Start listening
	err := http.ListenAndServe(fmt.Sprintf(":%s", port), nil)
	if err != nil {
		fmt.Println("Error:")
		fmt.Println(err)
	}
	fmt.Println()
	fmt.Println("gosigsrv exiting")
	if err != nil {
		os.Exit(2)
	} else {
		os.Exit(0)
	}
}
