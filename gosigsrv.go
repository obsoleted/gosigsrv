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
	return fmt.Sprintf("%s,%s,1", m.Name, m.ID)
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

func commonHeaderMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		setNoCacheHeader(res.Header())
		setVersionHeader(res.Header())
		addCorsHeaders(res.Header())
		setConnectionHeader(res.Header(), true)
		next.ServeHTTP(res, req)
	})
}

func signinHandler(res http.ResponseWriter, req *http.Request) {

	if req.Method != "GET" {
		http.Error(res, "Bad request", http.StatusBadRequest)
		return
	}
	var name string
	// Parse out peer name
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

	peers[peerInfo.ID] = &peerInfo

	setPragmaHeader(res.Header(), peerInfo.ID)

	peerInfoString := peerInfo.InfoString()
	peerInfoString += fmt.Sprintln()
	responseString := peerInfoString

	// Return above + current peers (filtered for oppositing type)
	for pID, pInfo := range peers {
		if pID != peerInfo.ID && pInfo.Kind != peerInfo.Kind && pInfo.ConnectedWith == "" {
			responseString += pInfo.InfoString()
			responseString += fmt.Sprintln()

			// Also notify these peers that the new one exists
			if len(pInfo.Channel) < cap(pInfo.Channel) {
				pInfo.Channel <- &peerMsg{pInfo.ID, peerInfoString}
			} else {
				fmt.Printf("WARNING: Dropped message for peer %s", pInfo)
				// TODO: Figure out what to do when peeer message buffer fills up
			}
		}
	}
	res.WriteHeader(http.StatusOK)
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

	setPragmaHeader(res.Header(), peerID)

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
	to.Channel <- &peerMsg{peerID, requestString}

	// Send message to channel for to id
	res.WriteHeader(http.StatusOK)
	fmt.Printf("message: %s -> %s: \n\t%s\n", from, to, requestString)
}

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
	peerInfo.LastContact = time.Now().UTC()
	peerInfo.Waiting = true

	fmt.Printf("wait: Peer %s waiting...\n", peerInfo)
	// Look up message channel for peers id
	// Wait for message to reply
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

	peerInfo.LastContact = time.Now().UTC()

	setPragmaHeader(res.Header(), peerMsg.FromID)
	res.WriteHeader(http.StatusOK)
	_, err := fmt.Fprint(res, peerMsg.Message)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
	}

	fmt.Printf("wait: Peer %s recieved message from ID %s\n\t%s\n\n", peerInfo, peerMsg.FromID, peerMsg.Message)
}

func peerCleanupRoutine() {
	tickerChan := time.NewTicker(time.Second * 30).C

	for {
		<-tickerChan
		fmt.Printf("Checking for stale peers\n")
		printStats()
		for k, v := range peers {
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
