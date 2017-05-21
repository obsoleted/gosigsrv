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

	peerInfoString := fmt.Sprintf("%s,%s,1", peerInfo.Name, peerInfo.ID)
	peerInfoString += fmt.Sprintln()
	responseString := peerInfoString

	// Return above + current peers (filtered for oppositing type)
	for pID, pInfo := range peers {
		if pID != peerInfo.ID && pInfo.Kind != peerInfo.Kind && pInfo.ConnectedWith == "" {
			responseString += fmt.Sprintf("%s,%s,1", pInfo.Name, pInfo.ID)
			responseString += fmt.Sprintln()

			// Also notify these peers that the new one exists
			if len(pInfo.Channel) < cap(pInfo.Channel) {
				pInfo.Channel <- &peerMsg{pInfo.ID, peerInfoString}
			} else {
				fmt.Printf("WARNING: Dropped message for peer %s[%s]", pInfo.Name, pInfo.ID)
				// TODO: Figure out what to do when peeer message buffer fills up
			}
		}
	}
	res.WriteHeader(http.StatusOK)
	_, err := fmt.Fprintf(res, responseString)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
	}
	fmt.Printf("sign-in - ClientName: %s, PeerId: %s\n", peerInfo.Name, peerInfo.ID)
	printStats()
}

func signoutHandler(res http.ResponseWriter, req *http.Request) {
	if req.Method != "GET" {
		http.Error(res, "Bad request", http.StatusBadRequest)
		return
	}
	var peerID string
	// Parse out peers id
	for k, v := range req.URL.Query() {
		if k == peerIDParamName {
			peerID = v[0]
		}
	}

	peer, exists := peers[peerID]
	if !exists {
		http.Error(res, "Unknown peer", http.StatusBadRequest)
		return
	}

	if peer.ConnectedWith != "" {
		connectedPeer, connectionExists := peers[peer.ConnectedWith]
		if connectionExists {
			connectedPeer.ConnectedWith = ""
		}
	}

	setPragmaHeader(res.Header(), peerID)
	delete(peers, peerID)
	res.WriteHeader(http.StatusOK)

	fmt.Printf("sign-out - ClientName: %s, PeerId: %s\n", peer.Name, peer.ID)
	printStats()
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

	from, peerInfoExists := peers[peerID[0]]
	to, toInfoExists := peers[toID[0]]

	if !peerInfoExists || !toInfoExists {
		http.Error(res, "Invalid Peer or To ID", http.StatusBadRequest)
		return
	}

	if from.ConnectedWith == "" {
		fmt.Printf("Connecting %s with %s\n", from.ID, to.ID)
		from.ConnectedWith = to.ID
	}

	if to.ConnectedWith == "" {
		fmt.Printf("Connecting %s with %s\n", to.ID, from.ID)
		to.ConnectedWith = from.ID
	}

	if from.ConnectedWith != to.ID {
		fmt.Printf("WARNING: Peer sending message to recipient outside room\n")
	}

	setPragmaHeader(res.Header(), peerID[0])

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
	to.Channel <- &peerMsg{peerID[0], requestString}

	// Send message to channel for to id
	res.WriteHeader(http.StatusOK)
	fmt.Printf("message: %s -> %s: \n\t%s\n", peerID[0], toID[0], requestString)
}

func waitHandler(res http.ResponseWriter, req *http.Request) {

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

	fmt.Printf("wait: Peer %s[%s] waiting...\n", peerInfo.Name, peerInfo.ID)
	// Look up message channel for peers id
	// Wait for message to reply
	peerMsg := <-peerInfo.Channel
	setPragmaHeader(res.Header(), peerMsg.FromID)
	res.WriteHeader(http.StatusOK)
	_, err := fmt.Fprint(res, peerMsg.Message)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
	}

	fmt.Printf("wait: Peer %s[%s] recieved message from %s\n%s\n", peerInfo.Name, peerInfo.ID, peerMsg.FromID, peerMsg.Message)
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
